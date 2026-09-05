package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopUpGroupCompletion(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousQuotaPerUnit := common.QuotaPerUnit
	previousPaymentSetting := *operation_setting.GetPaymentSetting()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &UserSubscription{}, &Log{}))
	DB, LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.QuotaPerUnit = 1000
	initCol()
	paymentSetting := operation_setting.GetPaymentSetting()
	paymentSetting.AutoSwitchGroupEnabled = true
	paymentSetting.AutoSwitchGroupOnlyNewTopups = true
	paymentSetting.AutoSwitchGroupEnabledFrom = common.GetTimestamp() - 60
	paymentSetting.AutoSwitchGroupBaseGroup = "default"
	paymentSetting.AutoSwitchGroupRules = []operation_setting.PaymentAutoSwitchGroupRule{
		{ThresholdUSD: 50, Group: "vip"},
	}
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.QuotaPerUnit = previousQuotaPerUnit
		*operation_setting.GetPaymentSetting() = previousPaymentSetting
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	useUserCacheMiniRedis(t)
}

func TestTopUpCompletionPreservesGroupSwitchAndWalletSafety(t *testing.T) {
	providers := []struct {
		name     string
		provider string
		method   string
		amount   int64
		complete func(*TopUp) error
	}{
		{"epay", PaymentProviderEpay, "alipay", 20, func(order *TopUp) error {
			_, err := RechargeEpay(order.TradeNo, "wxpay", "127.0.0.1")
			return err
		}},
		{"stripe", PaymentProviderStripe, PaymentMethodStripe, 20, func(order *TopUp) error {
			return Recharge(order.TradeNo, "customer", "127.0.0.1")
		}},
		{"creem", PaymentProviderCreem, PaymentMethodCreem, 20000, func(order *TopUp) error {
			return RechargeCreem(order.TradeNo, "", "", "127.0.0.1")
		}},
		{"waffo", PaymentProviderWaffo, PaymentMethodWaffo, 20, func(order *TopUp) error {
			return RechargeWaffo(order.TradeNo, "127.0.0.1")
		}},
		{"waffo-pancake", PaymentProviderWaffoPancake, PaymentMethodWaffoPancake, 20, func(order *TopUp) error {
			return RechargeWaffoPancake(order.TradeNo)
		}},
		{"manual", PaymentProviderEpay, "alipay", 20, func(order *TopUp) error {
			return ManualCompleteTopUp(order.TradeNo, "127.0.0.1")
		}},
	}
	for _, provider := range providers {
		for _, walletFull := range []bool{false, true} {
			name := provider.name + "/success"
			if walletFull {
				name = provider.name + "/wallet-limit"
			}
			t.Run(name, func(t *testing.T) {
				setupTopUpGroupCompletion(t)
				initialQuota := 7
				if walletFull {
					initialQuota = common.MaxWalletQuota - 1000
				}
				user := User{Username: "completion-user", Group: "default", Status: common.UserStatusEnabled, Quota: initialQuota}
				require.NoError(t, DB.Create(&user).Error)
				require.NoError(t, populateUserCache(user))
				require.NoError(t, DB.Create(&TopUp{
					UserId: user.Id, Amount: 30, Money: 30, TradeNo: "previous-payment",
					PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay,
					Status: common.TopUpStatusSuccess, CompleteTime: common.GetTimestamp() - 30,
				}).Error)
				order := TopUp{
					UserId: user.Id, Amount: provider.amount, Money: 20, TradeNo: "new-payment",
					PaymentMethod: provider.method, PaymentProvider: provider.provider, Status: common.TopUpStatusPending,
				}
				require.NoError(t, DB.Create(&order).Error)
				err := provider.complete(&order)
				expectedQuota, expectedGroup := initialQuota+20000, "vip"
				if walletFull {
					require.Error(t, err)
					expectedQuota, expectedGroup = initialQuota, "default"
				} else {
					require.NoError(t, err)
				}
				require.NoError(t, DB.First(&order, order.Id).Error)
				if walletFull {
					assert.Equal(t, common.TopUpStatusPending, order.Status)
					assert.Zero(t, order.CompleteTime)
				} else {
					assert.Equal(t, common.TopUpStatusSuccess, order.Status)
					assert.GreaterOrEqual(t, order.CompleteTime, operation_setting.GetPaymentSetting().AutoSwitchGroupEnabledFrom)
					if provider.name == "epay" {
						assert.Equal(t, "wxpay", order.PaymentMethod)
						alreadyDone, err := RechargeEpay(order.TradeNo, "wxpay", "127.0.0.1")
						require.NoError(t, err)
						assert.True(t, alreadyDone)
					}
				}
				require.NoError(t, DB.First(&user, user.Id).Error)
				assert.Equal(t, expectedQuota, user.Quota)
				assert.Equal(t, expectedGroup, user.Group)
				cached, err := cacheGetUserBase(user.Id)
				require.NoError(t, err)
				assert.Equal(t, expectedQuota, cached.Quota)
				assert.Equal(t, expectedGroup, cached.Group)
			})
		}
	}
}

func TestEpayCompletionRespectsGroupPolicy(t *testing.T) {
	cases := []struct {
		name           string
		currentGroup   string
		expectedGroup  string
		activePlan     bool
		disabled       bool
		oldPaymentOnly bool
	}{
		{name: "unrelated-manual-group", currentGroup: "staff", expectedGroup: "staff"},
		{name: "subscription-priority", currentGroup: "default", expectedGroup: "subscriber", activePlan: true},
		{name: "disabled-policy", currentGroup: "default", expectedGroup: "default", disabled: true},
		{name: "exclude-pre-activation-payment", currentGroup: "default", expectedGroup: "default", oldPaymentOnly: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			setupTopUpGroupCompletion(t)
			paymentSetting := operation_setting.GetPaymentSetting()
			paymentSetting.AutoSwitchGroupEnabled = !testCase.disabled
			user := User{Username: "group-policy-user", Group: testCase.currentGroup, Status: common.UserStatusEnabled}
			require.NoError(t, DB.Create(&user).Error)
			require.NoError(t, populateUserCache(user))
			if testCase.activePlan {
				require.NoError(t, DB.Create(&UserSubscription{
					UserId: user.Id, Status: "active", UpgradeGroup: "subscriber", EndTime: common.GetTimestamp() + 3600,
				}).Error)
			}
			amount := int64(60)
			if testCase.oldPaymentOnly {
				amount = 20
				require.NoError(t, DB.Create(&TopUp{
					UserId: user.Id, Amount: 40, TradeNo: "old-payment", PaymentMethod: "alipay",
					PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusSuccess,
					CompleteTime: paymentSetting.AutoSwitchGroupEnabledFrom - 1,
				}).Error)
			}
			order := TopUp{
				UserId: user.Id, Amount: amount, TradeNo: "policy-payment", PaymentMethod: "alipay",
				PaymentProvider: PaymentProviderEpay, Status: common.TopUpStatusPending,
			}
			require.NoError(t, DB.Create(&order).Error)
			alreadyDone, err := RechargeEpay(order.TradeNo, "alipay", "127.0.0.1")
			require.NoError(t, err)
			assert.False(t, alreadyDone)
			require.NoError(t, DB.First(&user, user.Id).Error)
			assert.Equal(t, int(amount)*1000, user.Quota)
			assert.Equal(t, testCase.expectedGroup, user.Group)
			cached, err := cacheGetUserBase(user.Id)
			require.NoError(t, err)
			assert.Equal(t, user.Quota, cached.Quota)
			assert.Equal(t, testCase.expectedGroup, cached.Group)
		})
	}
}
