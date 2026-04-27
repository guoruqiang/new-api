package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TopUp struct {
	Id              int     `json:"id"`
	UserId          int     `json:"user_id" gorm:"index"`
	Amount          int64   `json:"amount"`
	Money           float64 `json:"money"`
	TradeNo         string  `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod   string  `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider string  `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	CreateTime      int64   `json:"create_time"`
	CompleteTime    int64   `json:"complete_time"`
	Status          string  `json:"status"`
}

const (
	PaymentMethodStripe       = "stripe"
	PaymentMethodCreem        = "creem"
	PaymentMethodWaffo        = "waffo"
	PaymentMethodWaffoPancake = "waffo_pancake"
)

const (
	PaymentProviderEpay         = "epay"
	PaymentProviderStripe       = "stripe"
	PaymentProviderCreem        = "creem"
	PaymentProviderWaffo        = "waffo"
	PaymentProviderWaffoPancake = "waffo_pancake"
)

var (
	ErrPaymentMethodMismatch = errors.New("payment method mismatch")
	ErrTopUpNotFound         = errors.New("topup not found")
	ErrTopUpStatusInvalid    = errors.New("topup status invalid")
)

func normalizeTopUpPaymentMethod(method string) string {
	return strings.ToLower(strings.TrimSpace(method))
}

func requireTopUpPaymentMethod(expectedMethod string) func(topUp *TopUp) error {
	expectedMethod = normalizeTopUpPaymentMethod(expectedMethod)
	return func(topUp *TopUp) error {
		if topUp == nil {
			return errors.New("topup is nil")
		}
		if expectedMethod == "" {
			return nil
		}
		if normalizeTopUpPaymentMethod(topUp.PaymentMethod) != expectedMethod {
			return ErrPaymentMethodMismatch
		}
		return nil
	}
}

func requireTopUpPaymentProvider(expectedProvider string) func(topUp *TopUp) error {
	expectedProvider = normalizeTopUpPaymentMethod(expectedProvider)
	return func(topUp *TopUp) error {
		if topUp == nil {
			return errors.New("topup is nil")
		}
		if expectedProvider == "" {
			return nil
		}
		if normalizeTopUpPaymentMethod(topUp.PaymentProvider) != expectedProvider {
			return ErrPaymentMethodMismatch
		}
		return nil
	}
}

func combineTopUpValidators(validators ...func(topUp *TopUp) error) func(topUp *TopUp) error {
	return func(topUp *TopUp) error {
		for _, validator := range validators {
			if validator == nil {
				continue
			}
			if err := validator(topUp); err != nil {
				return err
			}
		}
		return nil
	}
}

func rejectTopUpPaymentMethods(disallowedMethods ...string) func(topUp *TopUp) error {
	disallowed := make(map[string]struct{}, len(disallowedMethods))
	for _, method := range disallowedMethods {
		normalized := normalizeTopUpPaymentMethod(method)
		if normalized == "" {
			continue
		}
		disallowed[normalized] = struct{}{}
	}
	return func(topUp *TopUp) error {
		if topUp == nil {
			return errors.New("topup is nil")
		}
		if _, exists := disallowed[normalizeTopUpPaymentMethod(topUp.PaymentMethod)]; exists {
			return ErrPaymentMethodMismatch
		}
		return nil
	}
}

func (topUp *TopUp) Insert() error {
	var err error
	err = DB.Create(topUp).Error
	return err
}

func (topUp *TopUp) Update() error {
	var err error
	err = DB.Save(topUp).Error
	return err
}

func GetTopUpById(id int) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("id = ?", id).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func GetTopUpByTradeNo(tradeNo string) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("trade_no = ?", tradeNo).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
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

func normalizePaymentAutoSwitchGroupBaseGroup(baseGroup string) string {
	trimmed := strings.TrimSpace(baseGroup)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func buildPaymentAutoSwitchGroupChainSet(paymentSetting *operation_setting.PaymentSetting) map[string]struct{} {
	if paymentSetting == nil {
		setting := operation_setting.GetPaymentSetting()
		paymentSetting = &setting
	}
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
	setting := operation_setting.GetPaymentSetting()
	return buildPaymentAutoSwitchGroupChainSet(&setting)
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

	return tx.Model(&User{}).Where("id = ?", userId).Update("group", targetGroup).Error
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
	if !common.UsingSQLite {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&group).Error; err != nil {
		return "", err
	}
	return strings.TrimSpace(group), nil
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

	chainGroups := buildPaymentAutoSwitchGroupChainSet(&paymentSetting)
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

func UpdatePendingTopUpStatus(tradeNo string, expectedPaymentProvider string, targetStatus string) error {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return ErrTopUpNotFound
		}
		if expectedPaymentProvider != "" && normalizeTopUpPaymentMethod(topUp.PaymentProvider) != normalizeTopUpPaymentMethod(expectedPaymentProvider) {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}

		topUp.Status = targetStatus
		return tx.Save(topUp).Error
	})
}

func completeTopUpTx(tx *gorm.DB, topUp *TopUp, userUpdates map[string]interface{}) (string, error) {
	if tx == nil || topUp == nil {
		return "", errors.New("invalid topup completion args")
	}

	topUp.CompleteTime = common.GetTimestamp()
	topUp.Status = common.TopUpStatusSuccess
	if err := tx.Save(topUp).Error; err != nil {
		return "", err
	}

	if len(userUpdates) > 0 {
		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Updates(userUpdates).Error; err != nil {
			return "", err
		}
	}

	return applyTopUpAutoSwitchGroupTx(tx, topUp.UserId)
}

func completeTopUpByTradeNo(
	tradeNo string,
	paymentMethodValidator func(topUp *TopUp) error,
	userUpdatesBuilder func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error),
	errPrefix string,
) (*TopUp, string, bool, error) {
	if tradeNo == "" {
		return nil, "", false, errors.New("未提供支付单号")
	}

	refCol := "`trade_no`"
	if common.UsingPostgreSQL {
		refCol = `"trade_no"`
	}

	var topUp TopUp
	var switchedGroup string
	completed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(&topUp).Error; err != nil {
			return errors.New("充值订单不存在")
		}
		if paymentMethodValidator != nil {
			if err := paymentMethodValidator(&topUp); err != nil {
				return err
			}
		}
		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		userUpdates := map[string]interface{}{}
		if userUpdatesBuilder != nil {
			updates, err := userUpdatesBuilder(&topUp, tx)
			if err != nil {
				return err
			}
			userUpdates = updates
		}

		var err error
		switchedGroup, err = completeTopUpTx(tx, &topUp, userUpdates)
		if err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil {
		if errPrefix != "" {
			common.SysError(errPrefix + ": " + err.Error())
		}
		return nil, "", false, err
	}
	return &topUp, switchedGroup, completed, nil
}

func Recharge(referenceId string, customerId string, callerIp string) (err error) {
	var quota int
	topUp, switchedGroup, completed, err := completeTopUpByTradeNo(referenceId, combineTopUpValidators(requireTopUpPaymentMethod(PaymentMethodStripe), requireTopUpPaymentProvider(PaymentProviderStripe)), func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error) {
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quota = int(decimal.NewFromFloat(topUp.Money).Mul(dQuotaPerUnit).IntPart())
		if quota <= 0 {
			return nil, errors.New("无效的充值额度")
		}
		return map[string]interface{}{
			"stripe_customer": customerId,
			"quota":           gorm.Expr("quota + ?", quota),
		}, nil
	}, "topup failed")
	if err != nil {
		return errors.New("充值失败，请稍后重试")
	}
	if switchedGroup != "" && topUp.UserId > 0 {
		_ = UpdateUserGroupCache(topUp.UserId, switchedGroup)
	}
	if !completed {
		return nil
	}

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%d", logger.FormatQuota(quota), topUp.Amount), callerIp, topUp.PaymentMethod, PaymentMethodStripe)

	return nil
}

// topUpQueryWindowSeconds 限制充值记录查询的时间窗口（秒）。
const topUpQueryWindowSeconds int64 = 30 * 24 * 60 * 60

// topUpQueryCutoff 返回允许查询的最早 create_time（秒级 Unix 时间戳）。
func topUpQueryCutoff() int64 {
	return common.GetTimestamp() - topUpQueryWindowSeconds
}

func GetUserTopUps(userId int, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	// Start transaction
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	cutoff := topUpQueryCutoff()

	// Get total count within transaction
	err = tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, cutoff).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated topups within same transaction
	err = tx.Where("user_id = ? AND create_time >= ?", userId, cutoff).Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// GetAllTopUps 获取全平台的充值记录（管理员使用，不限制时间窗口）
func GetAllTopUps(pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err = tx.Model(&TopUp{}).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// searchTopUpCountHardLimit 搜索充值记录时 COUNT 的安全上限，
// 防止对超大表执行无界 COUNT 触发 DoS。
const searchTopUpCountHardLimit = 10000

// SearchUserTopUps 按订单号搜索某用户的充值记录
func SearchUserTopUps(userId int, keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, topUpQueryCutoff())
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to count search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// SearchAllTopUps 按订单号搜索全平台充值记录（管理员使用，不限制时间窗口）
func SearchAllTopUps(keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{})
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to count search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// ManualCompleteTopUp 管理员手动完成订单并给用户充值
func ManualCompleteTopUp(tradeNo string, callerIp string) error {
	var quotaToAdd int
	topUp, switchedGroup, completed, err := completeTopUpByTradeNo(tradeNo, nil, func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error) {
		// 计算应充值额度：
		// - Stripe 订单：Money 代表经分组倍率换算后的美元数量，直接 * QuotaPerUnit
		// - 其他订单（如易支付）：Amount 为美元数量，* QuotaPerUnit
		if topUp.PaymentMethod == PaymentMethodStripe {
			dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
			quotaToAdd = int(decimal.NewFromFloat(topUp.Money).Mul(dQuotaPerUnit).IntPart())
		} else {
			dAmount := decimal.NewFromInt(topUp.Amount)
			dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
			quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		}
		if quotaToAdd <= 0 {
			return nil, errors.New("无效的充值额度")
		}
		return map[string]interface{}{
			"quota": gorm.Expr("quota + ?", quotaToAdd),
		}, nil
	}, "")
	if err != nil {
		return err
	}
	if switchedGroup != "" && topUp.UserId > 0 {
		_ = UpdateUserGroupCache(topUp.UserId, switchedGroup)
	}
	if !completed {
		return nil
	}

	// 事务外记录日志，避免阻塞
	RecordTopupLog(topUp.UserId, fmt.Sprintf("管理员补单成功，充值金额: %v，支付金额：%f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, "admin")
	return nil
}

func RechargeCreem(referenceId string, customerEmail string, customerName string, callerIp string) (err error) {
	var quota int64
	topUp, switchedGroup, completed, err := completeTopUpByTradeNo(referenceId, combineTopUpValidators(requireTopUpPaymentMethod(PaymentMethodCreem), requireTopUpPaymentProvider(PaymentProviderCreem)), func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error) {
		// Creem 直接使用 Amount 作为充值额度（整数）
		quota = topUp.Amount

		// 构建更新字段，优先使用邮箱，如果邮箱为空则使用用户名
		updateFields := map[string]interface{}{
			"quota": gorm.Expr("quota + ?", quota),
		}

		// 如果有客户邮箱，尝试更新用户邮箱（仅当用户邮箱为空时）
		if customerEmail != "" {
			// 先检查用户当前邮箱是否为空
			var user User
			if err := tx.Where("id = ?", topUp.UserId).First(&user).Error; err != nil {
				return nil, err
			}

			// 如果用户邮箱为空，则更新为支付时使用的邮箱
			if user.Email == "" {
				updateFields["email"] = customerEmail
			}
		}
		return updateFields, nil
	}, "creem topup failed")
	if err != nil {
		return errors.New("充值失败，请稍后重试")
	}
	if switchedGroup != "" && topUp.UserId > 0 {
		_ = UpdateUserGroupCache(topUp.UserId, switchedGroup)
	}
	if !completed {
		return nil
	}

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用Creem充值成功，充值额度: %v，支付金额：%.2f", quota, topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodCreem)

	return nil
}

func RechargeWaffo(tradeNo string, callerIp string) (err error) {
	var quotaToAdd int
	topUp, switchedGroup, completed, err := completeTopUpByTradeNo(tradeNo, combineTopUpValidators(requireTopUpPaymentMethod(PaymentMethodWaffo), requireTopUpPaymentProvider(PaymentProviderWaffo)), func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error) {
		dAmount := decimal.NewFromInt(topUp.Amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		if quotaToAdd <= 0 {
			return nil, errors.New("无效的充值额度")
		}
		return map[string]interface{}{
			"quota": gorm.Expr("quota + ?", quotaToAdd),
		}, nil
	}, "waffo topup failed")
	if err != nil {
		return errors.New("充值失败，请稍后重试")
	}
	if switchedGroup != "" && topUp != nil && topUp.UserId > 0 {
		_ = UpdateUserGroupCache(topUp.UserId, switchedGroup)
	}
	if !completed || topUp == nil {
		return nil
	}

	if quotaToAdd > 0 {
		RecordTopupLog(topUp.UserId, fmt.Sprintf("Waffo充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodWaffo)
	}

	return nil
}

func RechargeWaffoPancake(tradeNo string) (err error) {
	var quotaToAdd int
	topUp, switchedGroup, completed, err := completeTopUpByTradeNo(tradeNo, combineTopUpValidators(requireTopUpPaymentMethod(PaymentMethodWaffoPancake), requireTopUpPaymentProvider(PaymentProviderWaffoPancake)), func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error) {
		quotaToAdd = int(decimal.NewFromInt(topUp.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
		if quotaToAdd <= 0 {
			return nil, errors.New("无效的充值额度")
		}
		return map[string]interface{}{
			"quota": gorm.Expr("quota + ?", quotaToAdd),
		}, nil
	}, "waffo pancake topup failed")
	if err != nil {
		return errors.New("充值失败，请稍后重试")
	}
	if switchedGroup != "" && topUp != nil && topUp.UserId > 0 {
		_ = UpdateUserGroupCache(topUp.UserId, switchedGroup)
	}
	if !completed || topUp == nil {
		return nil
	}

	if quotaToAdd > 0 {
		RecordLog(topUp.UserId, LogTypeTopup, fmt.Sprintf("Waffo Pancake充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money))
	}

	return nil
}

func RechargeEpay(tradeNo string, callbackPaymentMethod string, callerIp string) (*TopUp, bool, error) {
	var quotaToAdd int
	validator := func(topUp *TopUp) error {
		if err := requireTopUpPaymentProvider(PaymentProviderEpay)(topUp); err != nil {
			return err
		}
		if err := rejectTopUpPaymentMethods(PaymentMethodStripe, PaymentMethodCreem, PaymentMethodWaffo, PaymentMethodWaffoPancake)(topUp); err != nil {
			return err
		}
		expectedPaymentMethod := normalizeTopUpPaymentMethod(callbackPaymentMethod)
		if expectedPaymentMethod != "" && normalizeTopUpPaymentMethod(topUp.PaymentMethod) != expectedPaymentMethod {
			return ErrPaymentMethodMismatch
		}
		return nil
	}
	topUp, switchedGroup, completed, err := completeTopUpByTradeNo(tradeNo, validator, func(topUp *TopUp, tx *gorm.DB) (map[string]interface{}, error) {
		dAmount := decimal.NewFromInt(topUp.Amount)
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quotaToAdd = int(dAmount.Mul(dQuotaPerUnit).IntPart())
		if quotaToAdd <= 0 {
			return nil, errors.New("无效的充值额度")
		}
		return map[string]interface{}{
			"quota": gorm.Expr("quota + ?", quotaToAdd),
		}, nil
	}, "epay topup failed")
	if err != nil {
		return nil, false, err
	}
	if switchedGroup != "" && topUp != nil && topUp.UserId > 0 {
		_ = UpdateUserGroupCache(topUp.UserId, switchedGroup)
	}
	if !completed || topUp == nil {
		return topUp, false, nil
	}

	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%f", logger.LogQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, callbackPaymentMethod)
	return topUp, true, nil
}
