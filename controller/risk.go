package controller

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
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

func GetRiskReviews(c *gin.Context) {
	filter := riskListFilter(c)
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !slices.Contains([]string{model.RiskReviewPending, model.RiskReviewConfirmed, model.RiskReviewDismissed, model.RiskReviewRevoked}, status) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid risk review status"))
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := model.ListRiskReviews(model.RiskReviewFilter{Namespace: filter.Namespace, UserId: filter.UserId, Status: status, RequestId: filter.RequestId}, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

// Risk review actions use the same administrator permission as other risk
// policy writes. Their only restrictions are namespace-scoped and temporary.
func GetRiskReview(c *gin.Context) {
	row, ok := readRiskReview(c)
	if !ok {
		return
	}
	snap, err := risk.BuildSnapshot()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	payload := risk.DefaultPolicyPayload()
	binding := snap.BindingFor(row.Namespace)
	if binding.Policy != nil {
		payload = binding.Policy.Payload
	}
	score, err := model.GetRiskBehaviorScore(row.Namespace, row.UserId, payload.Behavior.Policy(), common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	block, err := model.GetActiveRiskBlock(row.Namespace, risk.SubjectUser, strconv.Itoa(row.UserId), common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"review": row, "score": score, "block": block, "behavior_enabled": payload.Behavior.Enabled, "auto_block_enabled": payload.Behavior.AutoBlockEnabled, "mode": binding.Mode})
}

func readRiskReview(c *gin.Context) (*model.RiskReview, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid record id"))
		return nil, false
	}
	row, err := model.GetRiskReview(id)
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && row.DuplicateOf != 0 {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Risk review not found"))
		return nil, false
	}
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	return row, true
}

func ResolveRiskReview(c *gin.Context) {
	row, ok := readRiskReview(c)
	if !ok {
		return
	}
	var body struct {
		Revision int    `json:"revision"`
		Action   string `json:"action"`
		Category string `json:"category"`
		Note     string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid request body"))
		return
	}
	body.Note, body.Category = strings.TrimSpace(body.Note), strings.TrimSpace(body.Category)
	if body.Revision <= 0 || body.Note == "" || len([]rune(body.Note)) > 500 || !slices.Contains([]string{"confirm", "dismiss", "revoke"}, body.Action) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("A review action, revision and note of at most 500 characters are required"))
		return
	}
	if body.Action == "revoke" {
		// Revocation can release a derived restriction, so configuration
		// permission alone is insufficient. PAT scopes also constrain root.
		scope := service.AccessTokenScopeOf(authz.RiskUnblock)
		if c.GetBool("use_access_token") && !c.GetBool("access_token_legacy") &&
			!service.AccessTokenScopeGranted(c.GetStringSlice("access_token_scopes"), scope) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("The access token lacks the required permission: {{scope}}", map[string]any{"scope": scope}), gin.H{"code": "ACCESS_TOKEN_SCOPE_DENIED"})
			c.Abort()
			return
		}
		if !authz.Can(c.GetInt("id"), c.GetInt("role"), authz.RiskUnblock) {
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Unauthorized, insufficient privileges"))
			c.Abort()
			return
		}
	}
	if body.Action == "confirm" {
		if !slices.Contains([]string{"nsfw", "jailbreak", "reverse", "pentest", "cyber", "distill", "custom", "safety", "crack", "refusal"}, body.Category) {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid risk review category"))
			return
		}
		if strings.TrimSpace(row.Evidence) == "" {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Review evidence is unavailable; this incident cannot be confirmed"))
			return
		}
		if row.EvidenceTruncated {
			common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Stored review evidence is truncated; this incident cannot be confirmed"))
			return
		}
	}
	snap, err := risk.BuildSnapshot()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	binding := snap.BindingFor(row.Namespace)
	payload := risk.DefaultPolicyPayload()
	if binding.Policy != nil {
		payload = binding.Policy.Payload
	}
	if body.Action == "confirm" && !payload.Behavior.Enabled {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Behavior scoring is disabled for this group"))
		return
	}
	policy := payload.Behavior.Policy()
	policy.ExpectedChangeRevision = snap.ChangeRevision
	group, active := snap.NamespaceGroup[row.Namespace]
	active = active && snap.Scope.GroupNamespaces[group] == row.Namespace
	allowBlock := active && snap.Enabled && binding.Mode == risk.ModeEnforce && !snap.Scope.Exempt[row.Namespace] && !snap.Scope.Bypass[row.Namespace] && payload.Behavior.Enabled && payload.Behavior.AutoBlockEnabled
	result, err := model.ResolveRiskReview(row.Id, body.Revision, c.GetInt("id"), body.Action, body.Category, body.Note, policy, allowBlock, common.GetTimestamp())
	if errors.Is(err, model.ErrRiskReviewTransition) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("This risk review action is no longer available"))
		return
	}
	if err != nil {
		riskWriteError(c, err)
		return
	}
	// Clear this node's stale verdicts and refresh the revision immediately.
	// Other nodes follow the shared change revision through their poll loop.
	if err := risk.RefreshNow(); err != nil {
		common.ApiError(c, err)
		return
	}
	result.Block, err = model.GetActiveRiskBlock(row.Namespace, risk.SubjectUser, strconv.Itoa(row.UserId), common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"review": result.Review, "score": result.Score, "block": result.Block, "behavior_enabled": payload.Behavior.Enabled, "auto_block_enabled": payload.Behavior.AutoBlockEnabled, "mode": binding.Mode})
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
