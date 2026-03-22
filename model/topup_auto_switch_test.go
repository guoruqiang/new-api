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

	paymentSetting := operation_setting.GetPaymentSetting()
	originEnabled := paymentSetting.AutoSwitchGroupEnabled
	originRules := append([]operation_setting.PaymentAutoSwitchGroupRule(nil), paymentSetting.AutoSwitchGroupRules...)
	t.Cleanup(func() {
		paymentSetting.AutoSwitchGroupEnabled = originEnabled
		paymentSetting.AutoSwitchGroupRules = originRules
	})

	paymentSetting.AutoSwitchGroupEnabled = true
	paymentSetting.AutoSwitchGroupRules = []operation_setting.PaymentAutoSwitchGroupRule{
		{ThresholdUSD: 10, Group: "vip"},
	}
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
