package controller

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	optionValues := make(map[string]string)
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		value := common.Interface2String(v)
		if k == "payment_setting.auto_switch_group" {
			continue
		}
		if strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key") {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		for _, optionKey := range completionRatioMetaOptionKeys {
			if optionKey == k {
				optionValues[k] = value
				break
			}
		}
	}
	common.OptionMapRWMutex.Unlock()
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(optionValues),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
	return
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type normalizedPaymentAutoSwitchGroupRules struct {
	Rules []operation_setting.PaymentAutoSwitchGroupRule
	JSON  string
}

const paymentAutoSwitchGroupRulesRequiredMessage = "启用充值后自动切换分组前，请至少配置一条合法规则"

func isValidPaymentAutoSwitchGroup(group string) bool {
	if strings.TrimSpace(group) == "" {
		return false
	}
	_, ok := ratio_setting.GetGroupRatioCopy()[strings.TrimSpace(group)]
	return ok
}

func normalizePaymentAutoSwitchGroupRules(optionValue string) (*normalizedPaymentAutoSwitchGroupRules, error) {
	trimmed := strings.TrimSpace(optionValue)
	if trimmed == "" || trimmed == "null" {
		jsonBytes, err := common.Marshal([]operation_setting.PaymentAutoSwitchGroupRule{})
		if err != nil {
			return nil, err
		}
		return &normalizedPaymentAutoSwitchGroupRules{
			Rules: []operation_setting.PaymentAutoSwitchGroupRule{},
			JSON:  string(jsonBytes),
		}, nil
	}

	var rules []operation_setting.PaymentAutoSwitchGroupRule
	if err := common.UnmarshalJsonStr(trimmed, &rules); err != nil {
		return nil, fmt.Errorf("充值自动切换分组规则不是合法的 JSON 数组: %w", err)
	}

	normalizedRules := make([]operation_setting.PaymentAutoSwitchGroupRule, 0, len(rules))
	seenThresholds := make(map[string]struct{}, len(rules))
	for idx, rule := range rules {
		group := strings.TrimSpace(rule.Group)
		if math.IsNaN(rule.ThresholdUSD) || math.IsInf(rule.ThresholdUSD, 0) || rule.ThresholdUSD <= 0 {
			return nil, fmt.Errorf("第 %d 条充值自动切换分组规则的阈值必须大于 0", idx+1)
		}
		if !isValidPaymentAutoSwitchGroup(group) {
			return nil, fmt.Errorf("第 %d 条充值自动切换分组规则的分组不存在", idx+1)
		}

		thresholdKey := strconv.FormatFloat(rule.ThresholdUSD, 'f', -1, 64)
		if _, exists := seenThresholds[thresholdKey]; exists {
			return nil, fmt.Errorf("充值自动切换分组规则存在重复阈值: %s USD", thresholdKey)
		}
		seenThresholds[thresholdKey] = struct{}{}

		normalizedRules = append(normalizedRules, operation_setting.PaymentAutoSwitchGroupRule{
			ThresholdUSD: rule.ThresholdUSD,
			Group:        group,
		})
	}

	sort.Slice(normalizedRules, func(i, j int) bool {
		return normalizedRules[i].ThresholdUSD < normalizedRules[j].ThresholdUSD
	})

	jsonBytes, err := common.Marshal(normalizedRules)
	if err != nil {
		return nil, err
	}

	return &normalizedPaymentAutoSwitchGroupRules{
		Rules: normalizedRules,
		JSON:  string(jsonBytes),
	}, nil
}

func getRequestedPaymentAutoSwitchGroupRules(optionKey, optionValue string) ([]operation_setting.PaymentAutoSwitchGroupRule, error) {
	paymentSetting := operation_setting.GetPaymentSetting()
	if optionKey == "payment_setting.auto_switch_group_rules" {
		normalizedRules, err := normalizePaymentAutoSwitchGroupRules(optionValue)
		if err != nil {
			return nil, err
		}
		return normalizedRules.Rules, nil
	}

	rules := paymentSetting.AutoSwitchGroupRules
	if len(rules) == 0 {
		return []operation_setting.PaymentAutoSwitchGroupRule{}, nil
	}

	copiedRules := make([]operation_setting.PaymentAutoSwitchGroupRule, len(rules))
	copy(copiedRules, rules)
	return copiedRules, nil
}

func getRequestedPaymentAutoSwitchGroupEnabled(optionKey, optionValue string) bool {
	paymentSetting := operation_setting.GetPaymentSetting()
	if optionKey == "payment_setting.auto_switch_group_enabled" {
		enabled, err := strconv.ParseBool(optionValue)
		if err != nil {
			return false
		}
		return enabled
	}
	return paymentSetting.AutoSwitchGroupEnabled
}

func getRequestedPaymentAutoSwitchGroupOnlyNewTopups(optionKey, optionValue string) bool {
	paymentSetting := operation_setting.GetPaymentSetting()
	if optionKey == "payment_setting.auto_switch_group_only_new_topups" {
		onlyNewTopups, err := strconv.ParseBool(optionValue)
		if err != nil {
			return false
		}
		return onlyNewTopups
	}
	return paymentSetting.AutoSwitchGroupOnlyNewTopups
}

type requestedPaymentAutoSwitchGroupState struct {
	Enabled       bool
	OnlyNewTopups bool
	EnabledFrom   int64
}

func buildRequestedPaymentAutoSwitchGroupState(currentState operation_setting.PaymentSetting, optionKey, optionValue string) requestedPaymentAutoSwitchGroupState {
	requestedState := requestedPaymentAutoSwitchGroupState{
		Enabled:       currentState.AutoSwitchGroupEnabled,
		OnlyNewTopups: currentState.AutoSwitchGroupOnlyNewTopups,
		EnabledFrom:   currentState.AutoSwitchGroupEnabledFrom,
	}

	switch optionKey {
	case "payment_setting.auto_switch_group_enabled":
		enabled, err := strconv.ParseBool(optionValue)
		if err == nil {
			requestedState.Enabled = enabled
		}
	case "payment_setting.auto_switch_group_only_new_topups":
		onlyNewTopups, err := strconv.ParseBool(optionValue)
		if err == nil {
			requestedState.OnlyNewTopups = onlyNewTopups
		}
	}

	if !requestedState.Enabled || !requestedState.OnlyNewTopups {
		requestedState.EnabledFrom = 0
		return requestedState
	}

	if (!currentState.AutoSwitchGroupEnabled && requestedState.Enabled) ||
		(!currentState.AutoSwitchGroupOnlyNewTopups && requestedState.OnlyNewTopups) {
		requestedState.EnabledFrom = common.GetTimestamp() + 1
	}

	return requestedState
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！",
			})
			return
		}
	case "discord.enabled":
		if option.Value == "true" && system_setting.GetDiscordSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Discord OAuth，请先填入 Discord Client Id 以及 Discord Client Secret！",
			})
			return
		}
	case "oidc.enabled":
		if option.Value == "true" && system_setting.GetOIDCSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 OIDC 登录，请先填入 OIDC Client Id 以及 OIDC Client Secret！",
			})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！",
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用邮箱域名限制，请先填入限制的邮箱域名！",
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用微信登录，请先填入微信登录相关配置信息！",
			})
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！",
			})

			return
		}
	case "TelegramOAuthEnabled":
		if option.Value == "true" && common.TelegramBotToken == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Telegram OAuth，请先填入 Telegram Bot Token！",
			})
			return
		}
	case "GroupRatio":
		err = ratio_setting.CheckGroupRatio(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "图片倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频补全倍率设置失败: " + err.Error(),
			})
			return
		}
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "缓存创建倍率设置失败: " + err.Error(),
			})
			return
		}
	case "ModelRequestRateLimitGroup":
		err = setting.CheckModelRequestRateLimitGroup(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "payment_setting.auto_switch_group_rules":
		normalizedRules, normalizeErr := normalizePaymentAutoSwitchGroupRules(option.Value.(string))
		if normalizeErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": normalizeErr.Error(),
			})
			return
		}
		if getRequestedPaymentAutoSwitchGroupEnabled(option.Key, option.Value.(string)) && len(normalizedRules.Rules) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": paymentAutoSwitchGroupRulesRequiredMessage,
			})
			return
		}
		option.Value = normalizedRules.JSON
	case "payment_setting.auto_switch_group_enabled":
		enabled, parseErr := strconv.ParseBool(option.Value.(string))
		if parseErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的开关值",
			})
			return
		}
		if enabled {
			rules, rulesErr := getRequestedPaymentAutoSwitchGroupRules(option.Key, option.Value.(string))
			if rulesErr != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": rulesErr.Error(),
				})
				return
			}
			if len(rules) == 0 {
				c.JSON(http.StatusOK, gin.H{
					"success": false,
					"message": paymentAutoSwitchGroupRulesRequiredMessage,
				})
				return
			}
		}
	case "payment_setting.auto_switch_group_only_new_topups":
		if _, parseErr := strconv.ParseBool(option.Value.(string)); parseErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的开关值",
			})
			return
		}
	case "payment_setting.auto_switch_group_enabled_from":
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "不支持直接修改充值后自动切换分组的累计起始时间",
		})
		return
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	}
	optionValues := map[string]string{
		option.Key: option.Value.(string),
	}
	if option.Key == "payment_setting.auto_switch_group_enabled" || option.Key == "payment_setting.auto_switch_group_only_new_topups" {
		currentPaymentSetting := operation_setting.GetPaymentSetting()
		requestedState := buildRequestedPaymentAutoSwitchGroupState(currentPaymentSetting, option.Key, option.Value.(string))
		optionValues["payment_setting.auto_switch_group_enabled"] = strconv.FormatBool(requestedState.Enabled)
		optionValues["payment_setting.auto_switch_group_only_new_topups"] = strconv.FormatBool(requestedState.OnlyNewTopups)
		optionValues["payment_setting.auto_switch_group_enabled_from"] = strconv.FormatInt(requestedState.EnabledFrom, 10)
	}

	err = model.UpdateOptions(optionValues)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
