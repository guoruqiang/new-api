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

func TestApplyTopUpAutoSwitchGroupUsesSuccessfulCumulativeTopUps(t *testing.T) {
	previousDB := DB
	previousType := common.MainDatabaseType()
	previousRedisEnabled := common.RedisEnabled
	previousPaymentSetting := *operation_setting.GetPaymentSetting()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &UserSubscription{}))
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	paymentSetting := operation_setting.GetPaymentSetting()
	paymentSetting.AutoSwitchGroupEnabled = true
	paymentSetting.AutoSwitchGroupOnlyNewTopups = false
	paymentSetting.AutoSwitchGroupEnabledFrom = 0
	paymentSetting.AutoSwitchGroupBaseGroup = "default"
	paymentSetting.AutoSwitchGroupRules = []operation_setting.PaymentAutoSwitchGroupRule{
		{ThresholdUSD: 50, Group: "vip"},
		{ThresholdUSD: 100, Group: "premium"},
	}
	t.Cleanup(func() {
		*operation_setting.GetPaymentSetting() = previousPaymentSetting
		common.RedisEnabled = previousRedisEnabled
		DB = previousDB
		common.SetMainDatabaseType(previousType)
	})

	user := User{Username: "topup-switch-user", Group: "default", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId:          user.Id,
		Amount:          60,
		PaymentMethod:   "epay",
		PaymentProvider: PaymentProviderEpay,
		TradeNo:         "topup-switch-trade",
		Status:          common.TopUpStatusSuccess,
		CompleteTime:    common.GetTimestamp(),
	}).Error)

	switchedGroup, err := ApplyTopUpAutoSwitchGroup(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "vip", switchedGroup)
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, "vip", user.Group)
}
