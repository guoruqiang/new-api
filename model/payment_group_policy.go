package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
)

// NormalizeTopUpValueUSD converts successful ordinary top-ups into the USD
// amount used by the automatic group switching rules.
func NormalizeTopUpValueUSD(topUp *TopUp) float64 {
	if topUp == nil {
		return 0
	}

	switch normalizeTopUpPaymentMethod(topUp.PaymentMethod) {
	case PaymentMethodStripe:
		return topUp.Money
	case PaymentMethodCreem:
		if common.QuotaPerUnit <= 0 {
			return 0
		}
		return float64(topUp.Amount) / common.QuotaPerUnit
	default:
		return float64(topUp.Amount)
	}
}

func normalizePaymentAutoSwitchGroupBaseGroup(baseGroup string) string {
	trimmed := strings.TrimSpace(baseGroup)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func buildPaymentAutoSwitchGroupChainSet(paymentSetting operation_setting.PaymentSetting) map[string]struct{} {
	chainGroups := make(map[string]struct{}, len(paymentSetting.AutoSwitchGroupRules)+1)
	chainGroups[normalizePaymentAutoSwitchGroupBaseGroup(paymentSetting.AutoSwitchGroupBaseGroup)] = struct{}{}
	for _, rule := range paymentSetting.AutoSwitchGroupRules {
		group := strings.TrimSpace(rule.Group)
		if group == "" {
			continue
		}
		chainGroups[group] = struct{}{}
	}
	return chainGroups
}

func getPaymentAutoSwitchGroupChainSet() map[string]struct{} {
	return buildPaymentAutoSwitchGroupChainSet(operation_setting.GetPaymentSetting())
}

func isPaymentAutoSwitchGroupChainMember(group string, chainGroups map[string]struct{}) bool {
	trimmedGroup := strings.TrimSpace(group)
	if trimmedGroup == "" {
		return false
	}
	_, ok := chainGroups[trimmedGroup]
	return ok
}

func getPaymentAutoSwitchGroupTopUpCutoffTime() int64 {
	paymentSetting := operation_setting.GetPaymentSetting()
	if !paymentSetting.AutoSwitchGroupOnlyNewTopups || paymentSetting.AutoSwitchGroupEnabledFrom <= 0 {
		return 0
	}
	return paymentSetting.AutoSwitchGroupEnabledFrom
}

func GetUserSuccessfulTopupTotalUSDTx(tx *gorm.DB, userId int) (float64, error) {
	if tx == nil {
		return 0, errors.New("tx is nil")
	}
	if userId <= 0 {
		return 0, errors.New("invalid user id")
	}

	query := tx.Model(&TopUp{}).
		Select("amount", "money", "payment_method").
		Where("user_id = ? AND status = ? AND amount > 0", userId, common.TopUpStatusSuccess)
	if cutoffTime := getPaymentAutoSwitchGroupTopUpCutoffTime(); cutoffTime > 0 {
		query = query.Where("complete_time >= ?", cutoffTime)
	}

	var topUps []TopUp
	if err := query.Find(&topUps).Error; err != nil {
		return 0, err
	}

	totalUSD := 0.0
	for i := range topUps {
		totalUSD += NormalizeTopUpValueUSD(&topUps[i])
	}
	return totalUSD, nil
}

func matchPaymentAutoSwitchGroupRule(totalTopUpUSD float64, rules []operation_setting.PaymentAutoSwitchGroupRule) string {
	if totalTopUpUSD <= 0 || len(rules) == 0 {
		return ""
	}

	matchedGroup := ""
	matchedThreshold := -1.0
	for _, rule := range rules {
		group := strings.TrimSpace(rule.Group)
		if group == "" || rule.ThresholdUSD > totalTopUpUSD {
			continue
		}
		if rule.ThresholdUSD > matchedThreshold {
			matchedThreshold = rule.ThresholdUSD
			matchedGroup = group
		}
	}
	return matchedGroup
}

func getTopUpAutoSwitchTargetGroupTx(tx *gorm.DB, userId int) (string, error) {
	if tx == nil {
		return "", errors.New("tx is nil")
	}
	if userId <= 0 {
		return "", errors.New("invalid user id")
	}

	paymentSetting := operation_setting.GetPaymentSetting()
	if !paymentSetting.AutoSwitchGroupEnabled {
		return "", nil
	}

	totalTopUpUSD, err := GetUserSuccessfulTopupTotalUSDTx(tx, userId)
	if err != nil {
		return "", err
	}
	return matchPaymentAutoSwitchGroupRule(totalTopUpUSD, paymentSetting.AutoSwitchGroupRules), nil
}

func updateUserGroupTx(tx *gorm.DB, userId int, targetGroup string) error {
	if tx == nil {
		return errors.New("tx is nil")
	}
	if userId <= 0 {
		return errors.New("invalid user id")
	}

	targetGroup = strings.TrimSpace(targetGroup)
	if targetGroup == "" {
		return nil
	}

	return tx.Model(&User{}).Where("id = ?", userId).
		Update("group", targetGroup).Error
}

func applyTopUpAutoSwitchGroupTx(tx *gorm.DB, userId int) (string, error) {
	if tx == nil {
		return "", errors.New("tx is nil")
	}
	if userId <= 0 {
		return "", errors.New("invalid user id")
	}

	paymentSetting := operation_setting.GetPaymentSetting()
	if !paymentSetting.AutoSwitchGroupEnabled {
		return "", nil
	}

	currentGroup, err := getUserGroupByIdTx(tx, userId)
	if err != nil {
		return "", err
	}

	// 订阅升级分组生效期间，普通充值不能覆盖当前分组。
	activeUpgradeGroup, err := GetActiveSubscriptionUpgradeGroupTx(tx, userId, GetDBTimestamp())
	if err != nil {
		return "", err
	}
	if activeUpgradeGroup != "" {
		if currentGroup == activeUpgradeGroup {
			return "", nil
		}
		if err := updateUserGroupTx(tx, userId, activeUpgradeGroup); err != nil {
			return "", err
		}
		return activeUpgradeGroup, nil
	}

	chainGroups := buildPaymentAutoSwitchGroupChainSet(paymentSetting)
	if !isPaymentAutoSwitchGroupChainMember(currentGroup, chainGroups) {
		return "", nil
	}

	targetGroup, err := getTopUpAutoSwitchTargetGroupTx(tx, userId)
	if err != nil {
		return "", err
	}
	if targetGroup == "" || currentGroup == targetGroup {
		return "", nil
	}

	if err := updateUserGroupTx(tx, userId, targetGroup); err != nil {
		return "", err
	}
	return targetGroup, nil
}
