package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func normalizeTopUpPaymentMethod(method string) string {
	return strings.ToLower(strings.TrimSpace(method))
}

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

func buildPaymentAutoSwitchGroupChainSet(paymentSetting *operation_setting.PaymentSetting) map[string]struct{} {
	if paymentSetting == nil {
		paymentSetting = operation_setting.GetPaymentSetting()
	}
	chainGroups := make(map[string]struct{}, len(paymentSetting.AutoSwitchGroupRules)+1)
	chainGroups[operation_setting.NormalizePaymentAutoSwitchGroupBaseGroup(paymentSetting.AutoSwitchGroupBaseGroup)] = struct{}{}
	for _, rule := range paymentSetting.AutoSwitchGroupRules {
		group := strings.TrimSpace(rule.Group)
		if group != "" {
			chainGroups[group] = struct{}{}
		}
	}
	return chainGroups
}

func getPaymentAutoSwitchGroupChainSet() map[string]struct{} {
	return buildPaymentAutoSwitchGroupChainSet(operation_setting.GetPaymentSetting())
}

func isPaymentAutoSwitchGroupChainMember(group string, chainGroups map[string]struct{}) bool {
	_, ok := chainGroups[strings.TrimSpace(group)]
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
		Where(
			"user_id = ? AND status = ? AND ((LOWER(payment_method) = ? AND money > 0) OR (LOWER(payment_method) <> ? AND amount > 0))",
			userId,
			common.TopUpStatusSuccess,
			PaymentMethodStripe,
			PaymentMethodStripe,
		)
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
	matchedThreshold := 0.0
	matchedGroup := ""
	for _, rule := range rules {
		group := strings.TrimSpace(rule.Group)
		if group == "" || rule.ThresholdUSD <= 0 || rule.ThresholdUSD > totalTopUpUSD {
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

func getUserGroupForUpdateTx(tx *gorm.DB, userId int) (string, error) {
	if tx == nil {
		return "", errors.New("tx is nil")
	}
	if userId <= 0 {
		return "", errors.New("invalid user id")
	}
	var group string
	query := tx.Model(&User{}).Where("id = ?", userId).Select(commonGroupCol)
	if !common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&group).Error; err != nil {
		return "", err
	}
	return strings.TrimSpace(group), nil
}

func updateUserGroupTx(tx *gorm.DB, userId int, targetGroup string) error {
	targetGroup = strings.TrimSpace(targetGroup)
	if targetGroup == "" {
		return nil
	}
	return tx.Model(&User{}).Where("id = ?", userId).Update("group", targetGroup).Error
}

func getActiveSubscriptionUpgradeGroupTx(tx *gorm.DB, userId int, now int64, excludedSubscriptionId int) (string, error) {
	var activeSub UserSubscription
	query := tx.Where("user_id = ? AND status = ? AND end_time > ? AND upgrade_group <> ''", userId, "active", now)
	if excludedSubscriptionId > 0 {
		query = query.Where("id <> ?", excludedSubscriptionId)
	}
	result := query.Order("end_time desc, id desc").Limit(1).Find(&activeSub)
	if result.Error != nil || result.RowsAffected == 0 {
		return "", result.Error
	}
	return strings.TrimSpace(activeSub.UpgradeGroup), nil
}

func resolveUserEffectiveGroupTx(tx *gorm.DB, userId int, now int64, fallbackGroup string, excludedSubscriptionId int) (string, error) {
	activeUpgradeGroup, err := getActiveSubscriptionUpgradeGroupTx(tx, userId, now, excludedSubscriptionId)
	if err != nil {
		return "", err
	}
	if activeUpgradeGroup != "" {
		return activeUpgradeGroup, nil
	}
	fallbackGroup = strings.TrimSpace(fallbackGroup)
	chainGroups := getPaymentAutoSwitchGroupChainSet()
	if isPaymentAutoSwitchGroupChainMember(fallbackGroup, chainGroups) {
		topUpGroup, err := getTopUpAutoSwitchTargetGroupTx(tx, userId)
		if err != nil {
			return "", err
		}
		if topUpGroup != "" {
			return topUpGroup, nil
		}
	}
	return fallbackGroup, nil
}

func applyTopUpAutoSwitchGroupTx(tx *gorm.DB, userId int) (string, error) {
	paymentSetting := operation_setting.GetPaymentSetting()
	if !paymentSetting.AutoSwitchGroupEnabled {
		return "", nil
	}
	currentGroup, err := getUserGroupForUpdateTx(tx, userId)
	if err != nil {
		return "", err
	}
	activeUpgradeGroup, err := getActiveSubscriptionUpgradeGroupTx(tx, userId, GetDBTimestamp(), 0)
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
	if err != nil || targetGroup == "" || targetGroup == currentGroup {
		return "", err
	}
	if err := updateUserGroupTx(tx, userId, targetGroup); err != nil {
		return "", err
	}
	return targetGroup, nil
}

func ApplyTopUpAutoSwitchGroup(userId int) (string, error) {
	var switchedGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		switchedGroup, err = applyTopUpAutoSwitchGroupTx(tx, userId)
		return err
	})
	if err != nil {
		return "", err
	}
	if switchedGroup != "" {
		_ = UpdateUserGroupCache(userId, switchedGroup)
	}
	return switchedGroup, nil
}
