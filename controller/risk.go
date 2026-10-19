package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/risk"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func riskProblems(c *gin.Context, problems []*common.Message) {
	common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid risk audit settings"), gin.H{"problems": problems})
}

func riskConflict(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusConflict, common.NewMessage("The risk audit settings were changed by someone else. Reload and try again."), gin.H{"code": "revision_conflict"})
}

func riskWriteError(c *gin.Context, err error) {
	if errors.Is(err, model.ErrRiskRevisionConflict) {
		riskConflict(c)
		return
	}
	common.ApiError(c, err)
}

func riskTimeRange(c *gin.Context) (int64, int64) {
	now := time.Now()
	start, _ := strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end"), 10, 64)
	if start <= 0 {
		year, month, day := now.Date()
		start = time.Date(year, month, day, 0, 0, 0, 0, now.Location()).Unix()
	}
	if end <= 0 {
		end = now.Unix()
	}
	return start, end
}

func GetRiskOverview(c *gin.Context) {
	start, end := riskTimeRange(c)
	overview, err := risk.BuildOverview(start, end, strings.TrimSpace(c.Query("group")))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, overview)
}

func GetRiskDefaults(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"policy":                 risk.DefaultPolicyPayload(),
		"config":                 risk.DefaultConfigPayload(),
		"builtin_rules":          risk.BuiltinHardRules,
		"rule_categories":        risk.HardRuleCategories,
		"builtin_questions":      risk.BuiltinQuestions,
		"providers":              risk.ProviderPresets,
		"covered_routes":         risk.CoveredRoutes,
		"uncovered_routes":       risk.UncoveredRoutes,
		"policy_save_activation": true,
	})
}

func GetRiskConfig(c *gin.Context) {
	view, err := risk.LatestConfig()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func UpdateRiskConfig(c *gin.Context) {
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
		risk.ConfigPayload
	}
	body.ConfigPayload = risk.DefaultConfigPayload()
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	revision, problems, err := risk.PublishConfig(body.ExpectedRevision, body.ConfigPayload, c.GetInt("id"))
	if len(problems) > 0 {
		riskProblems(c, problems)
		return
	}
	if err != nil {
		riskWriteError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"revision": revision})
}

func GetRiskGroups(c *gin.Context) {
	dir, err := risk.ListGroupDirectory()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dir)
}

func GetRiskPolicies(c *gin.Context) {
	policies, err := model.ListRiskPolicies()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, policies)
}

func GetRiskPolicyRevisions(c *gin.Context) {
	rows, err := model.ListRiskPolicyRevisions(c.Param("key"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetRiskPolicy(c *gin.Context) {
	revision, err := strconv.Atoi(c.Param("revision"))
	if err != nil || revision <= 0 {
		common.ApiErrorT(c, "Invalid policy revision")
		return
	}
	view, err := risk.GetPolicy(c.Param("key"), revision)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Risk policy not found"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func CreateRiskPolicy(c *gin.Context) {
	var body struct {
		PolicyKey    string                 `json:"policy_key"`
		Name         string                 `json:"name"`
		BaseRevision int                    `json:"base_revision"`
		Payload      risk.PolicyPayload     `json:"payload"`
		Activation   *risk.PolicyActivation `json:"activation,omitempty"`
	}
	body.Payload = risk.DefaultPolicyPayload()
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	row, problems, err := risk.SavePolicyRevision(body.PolicyKey, body.Name, body.BaseRevision, body.Payload, c.GetInt("id"), body.Activation)
	if len(problems) > 0 {
		riskProblems(c, problems)
		return
	}
	if err != nil {
		riskWriteError(c, err)
		return
	}
	common.ApiSuccess(c, row)
}

func GetRiskProviders(c *gin.Context) {
	views, err := risk.ListProviderViews()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, views)
}

func saveRiskProvider(c *gin.Context, id int) {
	var body risk.ProviderInput
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	view, problems, err := risk.SaveProvider(id, body)
	if len(problems) > 0 {
		riskProblems(c, problems)
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Risk provider not found"))
		return
	}
	if err != nil {
		riskWriteError(c, err)
		return
	}
	common.ApiSuccess(c, view)
}

func CreateRiskProvider(c *gin.Context) {
	saveRiskProvider(c, 0)
}

func UpdateRiskProvider(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid provider id")
		return
	}
	saveRiskProvider(c, id)
}

func ProbeRiskProviderModels(c *gin.Context) {
	var body struct {
		ProviderID int    `json:"provider_id"`
		BaseURL    string `json:"base_url"`
		APIKey     string `json:"api_key"`
		Proxy      string `json:"proxy"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	url, models, err := risk.ProbeModels(c.Request.Context(), body.ProviderID, strings.TrimSpace(body.BaseURL), strings.TrimSpace(body.APIKey), strings.TrimSpace(body.Proxy))
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadGateway, err)
		return
	}
	common.ApiSuccess(c, gin.H{"url": url, "models": models})
}

func PreviewRisk(c *gin.Context) {
	var body risk.PreviewRequest
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	result, err := risk.Preview(c.Request.Context(), body)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func riskListFilter(c *gin.Context) model.RiskListFilter {
	userID, _ := strconv.Atoi(c.Query("user_id"))
	start, _ := strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end"), 10, 64)
	return model.RiskListFilter{
		Namespace:   strings.TrimSpace(c.Query("namespace")),
		GroupName:   strings.TrimSpace(c.Query("group")),
		UserId:      userID,
		Model:       strings.TrimSpace(c.Query("model")),
		SubjectType: strings.TrimSpace(c.Query("subject_type")),
		ActiveOnly:  c.Query("active") == "true",
		Layer:       strings.TrimSpace(c.Query("layer")),
		Outcome:     strings.TrimSpace(c.Query("outcome")),
		RequestId:   strings.TrimSpace(c.Query("request_id")),
		Start:       start,
		End:         end,
	}
}

func GetRiskEvents(c *gin.Context) {
	page := common.GetPageQuery(c)
	rows, total, err := model.ListRiskEvents(riskListFilter(c), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

func GetRiskEvent(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid event id")
		return
	}
	row, err := model.GetRiskEvent(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Risk event not found"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, row)
}

func GetRiskRefusals(c *gin.Context) {
	filter := riskListFilter(c)
	if filter.Namespace == "" {
		common.ApiErrorT(c, "A group namespace is required")
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := model.ListRiskRefusals(filter, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

func DeleteRiskRefusal(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid record id")
		return
	}
	removed, err := model.DeleteRiskRefusal(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !removed {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Refusal record not found"))
		return
	}
	common.ApiSuccess(c, gin.H{"removed": 1})
}

type riskClearRequest struct {
	Namespace string `json:"namespace"`
	UserID    int    `json:"user_id"`
	Confirm   bool   `json:"confirm"`
}

func readRiskClearRequest(c *gin.Context) (riskClearRequest, bool) {
	var body riskClearRequest
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return body, false
	}
	body.Namespace = strings.TrimSpace(body.Namespace)
	if body.Namespace == "" || !body.Confirm {
		common.ApiErrorT(c, "Clearing requires a group namespace and confirm: true")
		return body, false
	}
	if body.UserID < 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid user ID"))
		return body, false
	}
	return body, true
}

func ClearRiskRefusals(c *gin.Context) {
	body, ok := readRiskClearRequest(c)
	if !ok {
		return
	}
	removed, err := model.ClearRiskRefusals(body.Namespace, body.UserID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"removed": removed})
}

func GetRiskBlocks(c *gin.Context) {
	page := common.GetPageQuery(c)
	rows, total, err := model.ListRiskBlocks(riskListFilter(c), common.GetTimestamp(), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

func ReleaseRiskBlock(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid record id")
		return
	}
	released, err := model.ReleaseRiskBlock(id, c.GetInt("id"), common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !released {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Active block not found"))
		return
	}
	common.ApiSuccess(c, gin.H{"released": 1})
}

func ClearRiskBlocks(c *gin.Context) {
	body, ok := readRiskClearRequest(c)
	if !ok {
		return
	}
	if body.UserID != 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid user ID"))
		return
	}
	released, err := model.ClearRiskBlocks(body.Namespace, c.GetInt("id"), common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"released": released})
}
