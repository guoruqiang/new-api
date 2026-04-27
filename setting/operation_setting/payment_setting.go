package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type PaymentAutoSwitchGroupRule struct {
	ThresholdUSD float64 `json:"threshold_usd"`
	Group        string  `json:"group"`
}

type PaymentSetting struct {
	AmountOptions                []int                        `json:"amount_options"`
	AmountDiscount               map[int]float64              `json:"amount_discount"`
	AutoSwitchGroupEnabled       bool                         `json:"auto_switch_group_enabled"`
	AutoSwitchGroupOnlyNewTopups bool                         `json:"auto_switch_group_only_new_topups"`
	AutoSwitchGroupEnabledFrom   int64                        `json:"auto_switch_group_enabled_from"`
	AutoSwitchGroupBaseGroup     string                       `json:"auto_switch_group_base_group"`
	AutoSwitchGroupRules         []PaymentAutoSwitchGroupRule `json:"auto_switch_group_rules"`
}

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
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}
