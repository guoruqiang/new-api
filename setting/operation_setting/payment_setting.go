package operation_setting

import (
	"strings"

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
	ComplianceConfirmed          bool                         `json:"compliance_confirmed"`
	ComplianceTermsVersion       string                       `json:"compliance_terms_version"`
	ComplianceConfirmedAt        int64                        `json:"compliance_confirmed_at"`
	ComplianceConfirmedBy        int                          `json:"compliance_confirmed_by"`
	ComplianceConfirmedIP        string                       `json:"compliance_confirmed_ip"`
}

const CurrentComplianceTermsVersion = "v1"

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:            []int{10, 20, 50, 100, 200, 500},
	AmountDiscount:           map[int]float64{},
	AutoSwitchGroupBaseGroup: "default",
	AutoSwitchGroupRules:     []PaymentAutoSwitchGroupRule{},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

func NormalizePaymentAutoSwitchGroupBaseGroup(group string) string {
	group = strings.TrimSpace(group)
	if group == "" {
		return "default"
	}
	return group
}

func IsPaymentComplianceConfirmed() bool {
	return paymentSetting.ComplianceConfirmed &&
		paymentSetting.ComplianceTermsVersion == CurrentComplianceTermsVersion
}
