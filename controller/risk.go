package controller

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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
		"score_categories":       risk.ScoreCategories,
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
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	var body struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	cfg, parseErr := risk.ParseConfigPayload(raw)
	if parseErr != nil || common.Unmarshal(raw, &body) != nil {
		common.ApiErrorT(c, "Invalid request body")
		return
	}
	revision, problems, err := risk.PublishConfig(body.ExpectedRevision, cfg, c.GetInt("id"))
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
		Namespace: strings.TrimSpace(c.Query("namespace")),
		GroupName: strings.TrimSpace(c.Query("group")),
		UserId:    userID,
		Layer:     strings.TrimSpace(c.Query("layer")),
		Outcome:   strings.TrimSpace(c.Query("outcome")),
		RequestId: strings.TrimSpace(c.Query("request_id")),
		Start:     start,
		End:       end,
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

func GetRiskViolations(c *gin.Context) {
	userID, _ := strconv.Atoi(c.Query("user_id"))
	status := strings.TrimSpace(c.Query("status"))
	if status != "" && !slices.Contains([]string{model.RiskViolationPending, model.RiskViolationUpheld, model.RiskViolationRevoked}, status) {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("Invalid risk review status"))
		return
	}
	page := common.GetPageQuery(c)
	rows, total, err := model.ListRiskViolations(model.RiskViolationFilter{
		Status: status, UserId: userID, RequestId: strings.TrimSpace(c.Query("request_id")),
	}, page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(rows)
	common.ApiSuccess(c, page)
}

// riskViolationView is a violation with its user's current score and status.
func riskViolationView(c *gin.Context, row *model.RiskViolation) {
	score, err := model.GetRiskUserScore(row.UserId, common.GetTimestamp())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(row.UserId, false)
	userStatus := 0
	if err == nil {
		userStatus = user.Status
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{"violation": row, "score": score, "user_status": userStatus})
}

// readRiskViolation loads the violation named by the :id route parameter.
func readRiskViolation(c *gin.Context) (*model.RiskViolation, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return nil, false
	}
	row, err := model.GetRiskViolation(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Risk violation not found"))
		return nil, false
	}
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	return row, true
}

func GetRiskViolation(c *gin.Context) {
	if row, ok := readRiskViolation(c); ok {
		riskViolationView(c, row)
	}
}

// ResolveRiskViolation upholds a violation or revokes it, which returns its
// deduction to the user's score.
func ResolveRiskViolation(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var body struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || (body.Action != "uphold" && body.Action != "revoke") {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	body.Note = strings.TrimSpace(body.Note)
	if body.Note == "" || len([]rune(body.Note)) > 500 {
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("A note of at most 500 characters is required"))
		return
	}
	row, err := model.ResolveRiskViolation(id, c.GetInt("id"), body.Action, body.Note, common.GetTimestamp())
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("Risk violation not found"))
	case errors.Is(err, model.ErrRiskViolationTransition):
		common.ApiErrorStatus(c, http.StatusBadRequest, common.NewMessage("This risk review action is no longer available"))
	case err != nil:
		common.ApiError(c, err)
	default:
		riskViolationView(c, row)
	}
}

// BanRiskViolationUser disables or re-enables the account that committed a
// violation. Administrator accounts are never changed.
func BanRiskViolationUser(c *gin.Context) {
	row, ok := readRiskViolation(c)
	if !ok {
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := common.DecodeJson(c.Request.Body, &body); err != nil || (body.Action != "ban" && body.Action != "unban") {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	changed, err := service.SetUserEnabledForRisk(row.UserId, body.Action == "unban")
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ApiErrorT(c, "User does not exist")
	case err != nil:
		common.ApiError(c, err)
	case !changed:
		common.ApiErrorT(c, "No permission to update users of same or higher permission level")
	default:
		riskViolationView(c, row)
	}
}
