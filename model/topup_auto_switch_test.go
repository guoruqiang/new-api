package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prepareTopUpAutoSwitchTest(t *testing.T) {
	t.Helper()

	initCol()
	require.NoError(t, DB.AutoMigrate(&TopUp{}, &UserSubscription{}))

	DB.Exec("DELETE FROM top_ups")
	DB.Exec("DELETE FROM user_subscriptions")
	DB.Exec("DELETE FROM users")

	t.Cleanup(func() {
		DB.Exec("DELETE FROM top_ups")
		DB.Exec("DELETE FROM user_subscriptions")
		DB.Exec("DELETE FROM users")
	})

	originPaymentSetting := operation_setting.GetPaymentSetting()
	t.Cleanup(func() {
		operation_setting.UpdatePaymentSetting(func(setting *operation_setting.PaymentSetting) {
			*setting = originPaymentSetting
		})
	})

	operation_setting.UpdatePaymentSetting(func(setting *operation_setting.PaymentSetting) {
		setting.AutoSwitchGroupEnabled = true
		setting.AutoSwitchGroupOnlyNewTopups = false
		setting.AutoSwitchGroupEnabledFrom = 0
		setting.AutoSwitchGroupBaseGroup = "default"
		setting.AutoSwitchGroupRules = []operation_setting.PaymentAutoSwitchGroupRule{
			{ThresholdUSD: 10, Group: "vip"},
		}
	})
}

func TestApplyTopUpAutoSwitchGroupTx_SwitchesWithoutActiveSubscriptionUpgrade(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_no_sub",
		Password: "password123",
		Group:    "default",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	topup := &TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_topup_no_sub",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topup).Error)

	switchedGroup, err := applyTopUpAutoSwitchGroupTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, "vip", switchedGroup)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestApplyTopUpAutoSwitchGroupTx_DoesNotSwitchGroupOutsideControlledChain(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_outside_chain",
		Password: "password123",
		Group:    "sub",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	topup := &TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_topup_outside_chain",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topup).Error)

	switchedGroup, err := applyTopUpAutoSwitchGroupTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, "", switchedGroup)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "sub", reloaded.Group)
}

func TestApplyTopUpAutoSwitchGroupTx_UsesCustomBaseGroupForControlledChain(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	operation_setting.UpdatePaymentSetting(func(setting *operation_setting.PaymentSetting) {
		setting.AutoSwitchGroupBaseGroup = "sub"
	})

	user := &User{
		Username: "topup_custom_base_group",
		Password: "password123",
		Group:    "sub",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	topup := &TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_topup_custom_base_group",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topup).Error)

	switchedGroup, err := applyTopUpAutoSwitchGroupTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, "vip", switchedGroup)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestApplyTopUpAutoSwitchGroupTx_DoesNotOverrideActiveSubscriptionUpgrade(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_with_sub",
		Password: "password123",
		Group:    "zanzhu",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	topup := &TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_topup_with_sub",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topup).Error)

	subscription := &UserSubscription{
		UserId:        user.Id,
		PlanId:        1,
		Status:        "active",
		StartTime:     GetDBTimestamp() - 60,
		EndTime:       GetDBTimestamp() + 3600,
		UpgradeGroup:  "svip",
		PrevUserGroup: "default",
	}
	require.NoError(t, DB.Create(subscription).Error)

	switchedGroup, err := applyTopUpAutoSwitchGroupTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, "svip", switchedGroup)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "svip", reloaded.Group)
}

func TestExpireDueSubscriptions_FallsBackToTopUpGroupAfterSubscriptionEnds(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_after_sub_expire",
		Password: "password123",
		Group:    "svip",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	topup := &TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_topup_after_sub_expire",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topup).Error)

	now := GetDBTimestamp()
	subscription := &UserSubscription{
		UserId:        user.Id,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 3600,
		EndTime:       now - 1,
		UpgradeGroup:  "svip",
		PrevUserGroup: "default",
	}
	require.NoError(t, DB.Create(subscription).Error)

	expiredCount, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 1, expiredCount)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "vip", reloaded.Group)

	var reloadedSub UserSubscription
	require.NoError(t, DB.First(&reloadedSub, subscription.Id).Error)
	assert.Equal(t, "expired", reloadedSub.Status)
}

func TestGetUserSuccessfulTopupTotalUSDTx_DefaultModeCountsHistoricalTopups(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_total_default_mode",
		Password: "password123",
		Group:    "default",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        100,
		Money:         100,
		TradeNo:       "trade_total_default_mode_history",
		PaymentMethod: "epay",
		CompleteTime:  100,
		Status:        common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        1,
		Money:         1,
		TradeNo:       "trade_total_default_mode_new",
		PaymentMethod: "epay",
		CompleteTime:  200,
		Status:        common.TopUpStatusSuccess,
	}).Error)

	totalUSD, err := GetUserSuccessfulTopupTotalUSDTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 101.0, totalUSD)
}

func TestGetUserSuccessfulTopupTotalUSDTx_OnlyNewTopupsIgnoresHistoryBeforeEnabledFrom(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	operation_setting.UpdatePaymentSetting(func(setting *operation_setting.PaymentSetting) {
		setting.AutoSwitchGroupOnlyNewTopups = true
		setting.AutoSwitchGroupEnabledFrom = 150
	})

	user := &User{
		Username: "topup_total_only_new",
		Password: "password123",
		Group:    "default",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        100,
		Money:         100,
		TradeNo:       "trade_total_only_new_history",
		PaymentMethod: "epay",
		CompleteTime:  149,
		Status:        common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        1,
		Money:         1,
		TradeNo:       "trade_total_only_new_new",
		PaymentMethod: "epay",
		CompleteTime:  150,
		Status:        common.TopUpStatusSuccess,
	}).Error)

	totalUSD, err := GetUserSuccessfulTopupTotalUSDTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 1.0, totalUSD)
}

func TestApplyTopUpAutoSwitchGroupTx_OnlyNewTopupsUsesPostEnableAccumulation(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	operation_setting.UpdatePaymentSetting(func(setting *operation_setting.PaymentSetting) {
		setting.AutoSwitchGroupOnlyNewTopups = true
		setting.AutoSwitchGroupEnabledFrom = 150
		setting.AutoSwitchGroupRules = []operation_setting.PaymentAutoSwitchGroupRule{
			{ThresholdUSD: 1, Group: "vip"},
			{ThresholdUSD: 100, Group: "svip"},
		}
	})

	user := &User{
		Username: "topup_only_new_apply",
		Password: "password123",
		Group:    "default",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        100,
		Money:         100,
		TradeNo:       "trade_only_new_apply_history",
		PaymentMethod: "epay",
		CompleteTime:  149,
		Status:        common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        1,
		Money:         1,
		TradeNo:       "trade_only_new_apply_new",
		PaymentMethod: "epay",
		CompleteTime:  150,
		Status:        common.TopUpStatusSuccess,
	}).Error)

	switchedGroup, err := applyTopUpAutoSwitchGroupTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, "vip", switchedGroup)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestExpireDueSubscriptions_FallsBackToPrevGroupOutsideControlledChain(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_prev_group_outside_chain",
		Password: "password123",
		Group:    "svip",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_prev_group_outside_chain",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}).Error)

	now := GetDBTimestamp()
	subscription := &UserSubscription{
		UserId:        user.Id,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 3600,
		EndTime:       now - 1,
		UpgradeGroup:  "svip",
		PrevUserGroup: "sub",
	}
	require.NoError(t, DB.Create(subscription).Error)

	expiredCount, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 1, expiredCount)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "sub", reloaded.Group)
}

func TestExpireDueSubscriptions_OnlyNewTopupsFallbackUsesEnabledFromCutoff(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	operation_setting.UpdatePaymentSetting(func(setting *operation_setting.PaymentSetting) {
		setting.AutoSwitchGroupOnlyNewTopups = true
		setting.AutoSwitchGroupEnabledFrom = 150
		setting.AutoSwitchGroupRules = []operation_setting.PaymentAutoSwitchGroupRule{
			{ThresholdUSD: 1, Group: "vip"},
			{ThresholdUSD: 100, Group: "svip"},
		}
	})

	user := &User{
		Username: "topup_only_new_after_sub_expire",
		Password: "password123",
		Group:    "svip",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        100,
		Money:         100,
		TradeNo:       "trade_only_new_after_sub_expire_history",
		PaymentMethod: "epay",
		CompleteTime:  149,
		Status:        common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        1,
		Money:         1,
		TradeNo:       "trade_only_new_after_sub_expire_new",
		PaymentMethod: "epay",
		CompleteTime:  150,
		Status:        common.TopUpStatusSuccess,
	}).Error)

	now := GetDBTimestamp()
	subscription := &UserSubscription{
		UserId:        user.Id,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 3600,
		EndTime:       now - 1,
		UpgradeGroup:  "svip",
		PrevUserGroup: "default",
	}
	require.NoError(t, DB.Create(subscription).Error)

	expiredCount, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 1, expiredCount)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestMatchPaymentAutoSwitchGroupRule_UnsortedRules(t *testing.T) {
	rules := []operation_setting.PaymentAutoSwitchGroupRule{
		{ThresholdUSD: 50, Group: "svip"},
		{ThresholdUSD: 10, Group: "vip"},
		{ThresholdUSD: 20, Group: "pro"},
	}

	t.Run("matches highest eligible threshold from unsorted rules", func(t *testing.T) {
		assert.Equal(t, "svip", matchPaymentAutoSwitchGroupRule(80, rules))
	})

	t.Run("returns empty when total is below smallest threshold", func(t *testing.T) {
		assert.Equal(t, "", matchPaymentAutoSwitchGroupRule(5, rules))
	})

	t.Run("matches rule when total equals threshold exactly", func(t *testing.T) {
		assert.Equal(t, "pro", matchPaymentAutoSwitchGroupRule(20, rules))
	})
}

func TestExpireDueSubscriptions_UsesLatestEndedUpgradeSubscriptionForFallback(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_fallback_latest_end_time",
		Password: "password123",
		Group:    "svip",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_fallback_latest_end_time_topup",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}).Error)

	now := GetDBTimestamp()
	latestEndedSub := &UserSubscription{
		UserId:        user.Id,
		PlanId:        1,
		Status:        "active",
		StartTime:     now - 7200,
		EndTime:       now - 1,
		UpgradeGroup:  "svip",
		PrevUserGroup: "default",
	}
	require.NoError(t, DB.Create(latestEndedSub).Error)

	earlierEndedButNewerCreatedSub := &UserSubscription{
		UserId:        user.Id,
		PlanId:        2,
		Status:        "active",
		StartTime:     now - 3600,
		EndTime:       now - 10,
		UpgradeGroup:  "svip",
		PrevUserGroup: "sub",
	}
	require.NoError(t, DB.Create(earlierEndedButNewerCreatedSub).Error)

	expiredCount, err := ExpireDueSubscriptions(10)
	require.NoError(t, err)
	assert.Equal(t, 2, expiredCount)

	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	assert.Equal(t, "vip", reloaded.Group)

	var expiredSubs []UserSubscription
	require.NoError(t, DB.Where("user_id = ?", user.Id).Find(&expiredSubs).Error)
	require.Len(t, expiredSubs, 2)
	for _, sub := range expiredSubs {
		assert.Equal(t, "expired", sub.Status)
	}
}

func TestGetUserSuccessfulTopupTotalUSDTx_IgnoresZeroAmountShadowTopups(t *testing.T) {
	prepareTopUpAutoSwitchTest(t)

	user := &User{
		Username: "topup_total_ignores_shadow",
		Password: "password123",
		Group:    "default",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)

	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        20,
		Money:         20,
		TradeNo:       "trade_total_regular_topup",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:        user.Id,
		Amount:        0,
		Money:         999,
		TradeNo:       "trade_total_shadow_topup",
		PaymentMethod: "epay",
		Status:        common.TopUpStatusSuccess,
	}).Error)

	totalUSD, err := GetUserSuccessfulTopupTotalUSDTx(DB, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 20.0, totalUSD)
}

func TestNormalizeTopUpValueUSD(t *testing.T) {
	t.Run("epay uses amount as usd", func(t *testing.T) {
		assert.Equal(t, 10.0, NormalizeTopUpValueUSD(&TopUp{Amount: 10, Money: 99, PaymentMethod: "epay"}))
	})

	t.Run("stripe uses money as usd", func(t *testing.T) {
		assert.Equal(t, 9.99, NormalizeTopUpValueUSD(&TopUp{Amount: 999999, Money: 9.99, PaymentMethod: "stripe"}))
	})

	t.Run("creem converts quota to usd", func(t *testing.T) {
		require.Greater(t, common.QuotaPerUnit, 0.0)
		assert.Equal(t, 12.0, NormalizeTopUpValueUSD(&TopUp{Amount: int64(common.QuotaPerUnit * 12), Money: 1, PaymentMethod: "creem"}))
	})
}

func TestRequireTopUpPaymentMethod(t *testing.T) {
	validator := requireTopUpPaymentMethod("stripe")

	assert.NoError(t, validator(&TopUp{PaymentMethod: "stripe"}))
	assert.NoError(t, validator(&TopUp{PaymentMethod: " Stripe "}))
	assert.ErrorIs(t, validator(&TopUp{PaymentMethod: "creem"}), ErrPaymentMethodMismatch)
}

func TestRejectTopUpPaymentMethods(t *testing.T) {
	validator := rejectTopUpPaymentMethods("stripe", "creem", "waffo")

	assert.NoError(t, validator(&TopUp{PaymentMethod: "epay"}))
	assert.NoError(t, validator(&TopUp{PaymentMethod: "alipay"}))
	assert.ErrorIs(t, validator(&TopUp{PaymentMethod: "stripe"}), ErrPaymentMethodMismatch)
	assert.ErrorIs(t, validator(&TopUp{PaymentMethod: " Creem "}), ErrPaymentMethodMismatch)
	assert.ErrorIs(t, validator(&TopUp{PaymentMethod: "waffo"}), ErrPaymentMethodMismatch)
}
