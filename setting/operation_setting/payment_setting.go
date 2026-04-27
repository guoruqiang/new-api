package operation_setting

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/setting/config"
)

type PaymentAutoSwitchGroupRule struct {
	ThresholdUSD float64 `json:"threshold_usd"`
	Group        string  `json:"group"`
}

type PaymentSetting struct {
	AmountOptions                []int                        `json:"amount_options"`
	AmountDiscount               map[int]float64              `json:"amount_discount"` // 充值金额对应的折扣，例如 100 元 0.9 表示 100 元充值享受 9 折优惠
	AutoSwitchGroupEnabled       bool                         `json:"auto_switch_group_enabled"`
	AutoSwitchGroupOnlyNewTopups bool                         `json:"auto_switch_group_only_new_topups"`
	AutoSwitchGroupEnabledFrom   int64                        `json:"auto_switch_group_enabled_from"`
	AutoSwitchGroupBaseGroup     string                       `json:"auto_switch_group_base_group"`
	AutoSwitchGroupRules         []PaymentAutoSwitchGroupRule `json:"auto_switch_group_rules"`
}

var paymentSettingRWMutex sync.RWMutex

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:                []int{10, 20, 50, 100, 200, 500},
	AmountDiscount:               map[int]float64{},
	AutoSwitchGroupEnabled:       false,
	AutoSwitchGroupOnlyNewTopups: false,
	AutoSwitchGroupEnabledFrom:   0,
	AutoSwitchGroupBaseGroup:     "default",
	AutoSwitchGroupRules:         []PaymentAutoSwitchGroupRule{},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() PaymentSetting {
	paymentSettingRWMutex.RLock()
	defer paymentSettingRWMutex.RUnlock()

	return clonePaymentSettingLocked()
}

func normalizePaymentAutoSwitchGroupBaseGroup(baseGroup string) string {
	trimmed := strings.TrimSpace(baseGroup)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func UpdatePaymentSetting(mutator func(setting *PaymentSetting)) PaymentSetting {
	paymentSettingRWMutex.Lock()
	defer paymentSettingRWMutex.Unlock()

	if mutator != nil {
		mutator(&paymentSetting)
	}
	paymentSetting.AutoSwitchGroupBaseGroup = normalizePaymentAutoSwitchGroupBaseGroup(paymentSetting.AutoSwitchGroupBaseGroup)
	detachPaymentSettingLocked()
	return clonePaymentSettingLocked()
}

func detachPaymentSettingLocked() {
	paymentSetting.AmountOptions = append([]int(nil), paymentSetting.AmountOptions...)
	if paymentSetting.AmountDiscount != nil {
		amountDiscount := make(map[int]float64, len(paymentSetting.AmountDiscount))
		for amount, discount := range paymentSetting.AmountDiscount {
			amountDiscount[amount] = discount
		}
		paymentSetting.AmountDiscount = amountDiscount
	}
	paymentSetting.AutoSwitchGroupRules = append([]PaymentAutoSwitchGroupRule(nil), paymentSetting.AutoSwitchGroupRules...)
}

func clonePaymentSettingLocked() PaymentSetting {
	copiedSetting := paymentSetting
	copiedSetting.AmountOptions = append([]int(nil), paymentSetting.AmountOptions...)
	if paymentSetting.AmountDiscount != nil {
		copiedSetting.AmountDiscount = make(map[int]float64, len(paymentSetting.AmountDiscount))
		for amount, discount := range paymentSetting.AmountDiscount {
			copiedSetting.AmountDiscount[amount] = discount
		}
	}
	copiedSetting.AutoSwitchGroupRules = append([]PaymentAutoSwitchGroupRule(nil), paymentSetting.AutoSwitchGroupRules...)
	return copiedSetting
}
