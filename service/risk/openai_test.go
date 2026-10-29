package risk

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func moderationFixture() map[string]any {
	flags := make(map[string]any, len(OpenAIModerationCategories))
	scores := make(map[string]any, len(OpenAIModerationCategories))
	for _, category := range OpenAIModerationCategories {
		flags[category] = false
		scores[category] = 0.01
	}
	return map[string]any{
		"model": "omni-moderation-2024-09-26",
		"results": []any{map[string]any{
			"flagged": false, "categories": flags, "category_scores": scores,
		}},
	}
}

func TestOpenAIModerationProtocol(t *testing.T) {
	fixture := moderationFixture()
	row := fixture["results"].([]any)[0].(map[string]any)
	row["flagged"] = true
	row["categories"].(map[string]any)["sexual"] = true
	row["category_scores"].(map[string]any)["sexual"] = 0.2
	row["category_scores"].(map[string]any)["violence"] = 0.95
	body, err := common.Marshal(fixture)
	require.NoError(t, err)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		assert.Equal(t, "Bearer test-only-moderation-key", r.Header.Get("Authorization"))
		if r.URL.Path == "/v1/models" {
			assert.Equal(t, http.MethodGet, r.Method)
			_, _ = io.WriteString(w, `{"data":[{"id":"omni-moderation-latest"}]}`)
			return
		}
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var payload map[string]any
		if !assert.NoError(t, common.DecodeJson(r.Body, &payload)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, map[string]any{"model": "omni-moderation-latest", "input": "current user text"}, payload)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	for _, suffix := range []string{"", "/v1/", "/v1/moderations"} {
		rt := newProviderRuntime(ProviderConfig{Type: "openai", BaseURL: server.URL + suffix, KeyLines: []string{"test-only-moderation-key"}})
		result, err := rt.Classify(context.Background(), ClassifyRequest{
			State: "current user text", Questions: []askedQuestion{{id: "custom", instructions: "Never send this instruction"}}, MaxAttempts: 1,
		})
		require.NoError(t, err)
		assert.True(t, result.Flags["sexual"], "official flags must not be replaced with a 0.5 score threshold")
		assert.False(t, result.Flags["violence"])
		assert.InDelta(t, 0.8, result.Scores["sexual"], 1e-12)
		assert.InDelta(t, 0.05, result.Scores["violence"], 1e-12)
		assert.Equal(t, "omni-moderation-2024-09-26", result.Model)
		assert.NotContains(t, result.Scores, "custom")
	}
	url, models, err := ListModels(context.Background(), server.URL+"/v1/moderations", "test-only-moderation-key", "")
	require.NoError(t, err)
	assert.Equal(t, server.URL+"/v1/models", url)
	assert.Equal(t, []string{"omni-moderation-latest"}, models)
	assert.Equal(t, []string{"/v1/moderations", "/v1/moderations", "/v1/moderations", "/v1/models"}, paths)
	assert.Empty(t, OpenAIModerationURL("https://user:secret@example.test"))
	assert.Empty(t, OpenAIModerationURL("https://example.test?api_key=secret"))
	assert.Empty(t, OpenAIModerationURL("file:///tmp/moderations"))
	input := ProviderInput{Name: "OpenAI", ProviderType: "openai", BaseURL: server.URL, Model: "omni-moderation-latest"}
	assert.Empty(t, validateProvider(input))
	input.Model = "english"
	assert.NotEmpty(t, validateProvider(input), "changing an existing ModernBERT provider must not retain its incompatible model")
	input.Model, input.BaseURL = "", server.URL+"?api_key=secret"
	assert.NotEmpty(t, validateProvider(input))
	rt := newProviderRuntime(ProviderConfig{Type: "openai", BaseURL: server.URL})
	before := len(paths)
	_, err = rt.Classify(context.Background(), ClassifyRequest{State: "text", Model: "jev-latest", MaxAttempts: 1})
	require.Error(t, err)
	assert.Equal(t, before, len(paths), "a stale policy model override must fail without sending an incompatible request")
}

func TestOpenAIModerationMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"missing result", func(doc, _ map[string]any) { delete(doc, "results") }},
		{"multiple results", func(doc, row map[string]any) { doc["results"] = []any{row, row} }},
		{"missing model", func(doc, _ map[string]any) { delete(doc, "model") }},
		{"incompatible model", func(doc, _ map[string]any) { doc["model"] = "chat-model" }},
		{"oversized model cannot be retained", func(doc, _ map[string]any) { doc["model"] = "omni-moderation-" + strings.Repeat("x", 121) }},
		{"missing flagged", func(_, row map[string]any) { delete(row, "flagged") }},
		{"null flag", func(_, row map[string]any) { row["categories"].(map[string]any)["sexual"] = nil }},
		{"missing category", func(_, row map[string]any) { delete(row["categories"].(map[string]any), "illicit") }},
		{"missing score", func(_, row map[string]any) { delete(row["category_scores"].(map[string]any), "violence") }},
		{"null score", func(_, row map[string]any) { row["category_scores"].(map[string]any)["sexual"] = nil }},
		{"wrong type", func(_, row map[string]any) { row["category_scores"].(map[string]any)["sexual"] = "0.9" }},
		{"negative score", func(_, row map[string]any) { row["category_scores"].(map[string]any)["sexual"] = -0.1 }},
		{"large score", func(_, row map[string]any) { row["category_scores"].(map[string]any)["sexual"] = 1.1 }},
		{"flagged without category", func(_, row map[string]any) { row["flagged"] = true }},
		{"category without flagged", func(_, row map[string]any) { row["categories"].(map[string]any)["sexual"] = true }},
		{"unsupported flagged category", func(_, row map[string]any) {
			row["flagged"] = true
			row["categories"].(map[string]any)["future-category"] = true
			row["category_scores"].(map[string]any)["future-category"] = 0.9
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := moderationFixture()
			row := fixture["results"].([]any)[0].(map[string]any)
			tc.mutate(fixture, row)
			body, err := common.Marshal(fixture)
			require.NoError(t, err)
			result, err := parseOpenAIModeration(body)
			require.Error(t, err)
			assert.Empty(t, result.Scores)
			assert.Empty(t, result.Flags)
		})
	}
	for _, body := range []string{`null`, `<html>private-key</html>`, `{"model":"x","results":[null]}`, `{"model":"x","results":[]}`} {
		_, err := parseOpenAIModeration([]byte(body))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private-key")
	}
}

func TestOpenAIModerationKeyRotationAndFailures(t *testing.T) {
	body, err := common.Marshal(moderationFixture())
	require.NoError(t, err)
	var attempts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		attempts = append(attempts, key)
		switch key {
		case "rejected":
			w.WriteHeader(http.StatusUnauthorized)
		case "busy":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		case "unavailable":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "private-credential-and-request")
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(server.Close)
	rt := newProviderRuntime(ProviderConfig{Type: "openai", BaseURL: server.URL, KeyLines: []string{"rejected", "busy", "good"}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	result, err := rt.Classify(ctx, ClassifyRequest{State: "test", MaxAttempts: 3})
	require.NoError(t, err)
	assert.Len(t, result.Flags, len(OpenAIModerationCategories))
	assert.Equal(t, []string{"rejected", "busy", "good"}, attempts)
	assert.True(t, rt.unusable(rt.targets[0].id))
	assert.True(t, rt.unusable(rt.targets[1].id))
	deadline, _ := ctx.Deadline()
	assert.False(t, rt.coolUntil[rt.targets[1].id].After(deadline))
	rt = newProviderRuntime(ProviderConfig{Type: "openai", BaseURL: server.URL, KeyLines: []string{"unavailable"}})
	_, err = rt.Classify(context.Background(), ClassifyRequest{State: "test", MaxAttempts: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.NotContains(t, err.Error(), "private-credential-and-request")
	canceled, stop := context.WithCancel(context.Background())
	stop()
	before := len(attempts)
	_, err = rt.Classify(canceled, ClassifyRequest{State: "test", MaxAttempts: 1})
	require.Error(t, err)
	assert.Equal(t, before, len(attempts))
}

func TestOpenAIModerationEngineDecisions(t *testing.T) {
	oldHistory := strings.Repeat("ordinary history ", 20000)
	for _, tc := range []struct {
		name, category, failAction, raw, outcome, reason string
		flags                                            map[string]bool
		scores                                           map[string]float64
		automatic, incomplete, observe                   bool
		calls, assessments                               int
	}{
		{name: "normal request never calls the provider", raw: `{"model":"claude","input":"normal question"}`, outcome: OutcomePass, reason: ReasonModelNotTriggered},
		{name: "fresh confirmed sexual content", flags: map[string]bool{"sexual": true}, scores: map[string]float64{"sexual": .99}, outcome: OutcomeBlock, automatic: true, calls: 1, assessments: 1},
		{name: "official flag blocks below score threshold", flags: map[string]bool{"sexual": true}, scores: map[string]float64{"sexual": .2}, outcome: OutcomeBlock, calls: 1, assessments: 1},
		{name: "official clear flag wins over high risk score", scores: map[string]float64{"sexual": .99}, outcome: OutcomePass, reason: ReasonRuleCleared, calls: 1},
		{name: "multiple categories still deduct twenty once", flags: map[string]bool{"sexual": true, "violence": true}, scores: map[string]float64{"sexual": .99, "violence": .98}, outcome: OutcomeBlock, automatic: true, calls: 1, assessments: 2},
		{name: "all official flags survive grouping", flags: map[string]bool{"sexual": true, "sexual/minors": true}, scores: map[string]float64{"sexual": .99, "sexual/minors": .98}, outcome: OutcomeBlock, automatic: true, calls: 1, assessments: 1},
		{name: "old assistant clipping does not fail current content", raw: `{"model":"claude","messages":[{"role":"assistant","content":"` + oldHistory + `"},{"role":"user","content":"risk-signal latest user"}]}`, failAction: FailActionBlock, outcome: OutcomePass, reason: ReasonRuleCleared, calls: 1},
		{name: "old user clipping does not fail current content", raw: `{"model":"claude","messages":[{"role":"user","content":"` + oldHistory + `"},{"role":"user","content":"risk-signal latest user"}]}`, failAction: FailActionBlock, outcome: OutcomePass, reason: ReasonRuleCleared, calls: 1},
		{name: "current text clipping is still incomplete", raw: `{"model":"claude","input":"` + oldHistory + `risk-signal latest user"}`, failAction: FailActionBlock, outcome: OutcomeErrorBlock, reason: ReasonProviderUnsupported, calls: 1},
		{name: "jailbreak unsupported with fail open", category: "jailbreak", outcome: OutcomeErrorPass, reason: ReasonProviderUnsupported, calls: 1},
		{name: "jailbreak unsupported with fail closed", category: "jailbreak", failAction: FailActionBlock, outcome: OutcomeErrorBlock, reason: ReasonProviderUnsupported, calls: 1},
		{name: "custom rules are not covered", category: "custom", outcome: OutcomeErrorPass, reason: ReasonProviderUnsupported, calls: 1},
		{name: "model flag triggered by context only cannot auto confirm", raw: `{"model":"claude","messages":[{"role":"system","content":"risk-signal context"},{"role":"user","content":"normal question"}]}`, flags: map[string]bool{"sexual": true}, scores: map[string]float64{"sexual": .99}, outcome: OutcomeBlock, calls: 1, assessments: 1},
		{name: "context signal cannot be declared cleared", raw: `{"model":"claude","messages":[{"role":"assistant","content":"risk-signal context"},{"role":"user","content":"normal question"}]}`, outcome: OutcomeErrorPass, reason: ReasonProviderUnsupported, calls: 1},
		{name: "unknown media cannot authorize penalty", flags: map[string]bool{"sexual": true}, scores: map[string]float64{"sexual": .99}, incomplete: true, outcome: OutcomeBlock, calls: 1, assessments: 1},
		{name: "unknown media cannot be declared cleared", incomplete: true, outcome: OutcomeErrorPass, reason: ReasonProviderUnsupported, calls: 1},
		{name: "observe does not confirm", flags: map[string]bool{"sexual": true}, scores: map[string]float64{"sexual": .99}, observe: true, outcome: OutcomeWouldBlock, calls: 1, assessments: 1},
		{name: "context without user text cannot be moderated", raw: `{"model":"claude","messages":[{"role":"system","content":"risk-signal context"}]}`, outcome: OutcomeErrorPass, reason: ReasonProviderUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := moderationFixture()
			row := fixture["results"].([]any)[0].(map[string]any)
			for category, flag := range tc.flags {
				row["categories"].(map[string]any)[category] = flag
				if flag {
					row["flagged"] = true
				}
			}
			for category, score := range tc.scores {
				row["category_scores"].(map[string]any)[category] = score
			}
			body, err := common.Marshal(fixture)
			require.NoError(t, err)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, "/v1/moderations", r.URL.Path)
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)
			p := DefaultPolicyPayload()
			p.Distill.Enabled, p.Refusal.Enabled = false, false
			p.Model.Enabled, p.Model.ProviderID, p.Model.DedupSec = true, 1, 0
			p.Behavior.AutoConfirmEnabled, p.Behavior.AutoBlockEnabled = true, true
			category := tc.category
			if category == "" {
				category = "nsfw"
			}
			p.HardRegex.Rules = []HardRule{{Category: category, Source: "risk-signal", Enabled: true, Action: RuleActionReview}}
			if tc.failAction != "" {
				p.Model.FailAction = tc.failAction
			}
			raw := tc.raw
			if raw == "" {
				raw = `{"model":"claude","api_key":"private-auth-test","input":"risk-signal latest user"}`
			}
			in := auditInput(t, raw)
			in.HasMedia = tc.incomplete
			ev := evaluationFor(t, p, in)
			if tc.observe {
				ev.binding.Mode = ModeObserve
			}
			ev.snapshot.Providers[1] = ProviderConfig{ID: 1, Type: "openai", BaseURL: server.URL}
			ev.classifier = func(cfg ProviderConfig) Classifier { return newProviderRuntime(cfg) }
			d := applyMode(evaluate(context.Background(), ev), ev.binding.Mode)
			assert.Equal(t, tc.outcome, d.Outcome)
			assert.EqualValues(t, tc.calls, calls.Load())
			if tc.reason != "" {
				assert.Equal(t, tc.reason, d.Reason)
			}
			commit(ev, d)
			store := ev.store.(*memoryStore)
			if tc.assessments > 0 {
				require.Len(t, store.reviews, 1)
				assert.Len(t, d.Assessments, tc.assessments)
				assert.Equal(t, 20, d.Deduction)
				assert.Equal(t, 20, store.reviews[0].Deduction)
				assert.Equal(t, []bool{tc.automatic}, store.autoConfirmed)
				assert.Equal(t, "omni-moderation-2024-09-26", store.reviews[0].ProviderModel)
				assert.Contains(t, store.reviews[0].Assessments, "OpenAI Moderation flagged sexual")
				for category, flagged := range tc.flags {
					if flagged {
						assert.Contains(t, store.reviews[0].Assessments, category)
					}
				}
				assert.NotEmpty(t, store.reviews[0].Evidence)
				assert.NotContains(t, store.reviews[0].OriginalRequest, "private-auth-test")
			} else {
				assert.Empty(t, store.reviews)
				assert.Empty(t, d.Assessments)
				assert.Empty(t, d.Preview)
				assert.Empty(t, d.ReviewEvidence)
				assert.Empty(t, d.Keyword)
				assert.False(t, d.AutoConfirm)
				assert.Zero(t, d.Deduction)
			}
		})
	}
}

func TestOpenAIModerationCurrentUserEvidenceAndRetry(t *testing.T) {
	fixture := moderationFixture()
	row := fixture["results"].([]any)[0].(map[string]any)
	row["flagged"] = true
	row["categories"].(map[string]any)["sexual"] = true
	row["category_scores"].(map[string]any)["sexual"] = .99
	body, err := common.Marshal(fixture)
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]any
		if !assert.NoError(t, common.DecodeJson(r.Body, &payload)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, map[string]any{"model": "omni-moderation-latest", "input": "risk-signal latest user"}, payload,
			"system, previous user, assistant and tool text must not become moderation input")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	p := DefaultPolicyPayload()
	p.Distill.Enabled, p.Refusal.Enabled = false, false
	p.Model.Enabled, p.Model.ProviderID = true, 1
	p.Behavior.AutoConfirmEnabled = true
	p.HardRegex.Rules = []HardRule{{Category: "nsfw", Source: "risk-signal", Enabled: true, Action: RuleActionReview}}
	base := `{"model":"claude","messages":[{"role":"system","content":"system context"},{"role":"user","content":"old user"},{"role":"assistant","content":"assistant context"},{"role":"tool","content":"tool-one"},{"role":"user","content":"risk-signal latest user"}]}`
	ev := evaluationFor(t, p, auditInput(t, base))
	ev.attempt.Namespace = t.Name()
	ev.snapshot.Providers[1] = ProviderConfig{ID: 1, Type: "openai", BaseURL: server.URL}
	ev.classifier = func(cfg ProviderConfig) Classifier { return newProviderRuntime(cfg) }
	ev.useCache = true
	for i, raw := range []string{base, base, strings.Replace(base, "tool-one", "tool-two", 1)} {
		ev.input = auditInput(t, raw)
		ev.attempt.RequestID = []string{"first", "retry", "next-tool"}[i]
		d := evaluate(context.Background(), ev)
		assert.Equal(t, OutcomeBlock, d.Outcome)
		assert.Equal(t, i != 1, d.AutoConfirm)
		assert.Equal(t, "sexual", d.Keyword)
		assert.Equal(t, "omni-moderation-2024-09-26", d.ProviderModel)
		assert.Equal(t, "risk-signal latest user", d.ReviewEvidence)
		commit(ev, d)
	}
	assert.EqualValues(t, 2, calls.Load(), "identical retry reuses flags; changed context requests its own model verdict")
	store := ev.store.(*memoryStore)
	require.Len(t, store.reviews, 3)
	assert.Equal(t, []bool{true, false, true}, store.autoConfirmed)
	assert.NotEmpty(t, store.reviews[0].IncidentKey)
	assert.Equal(t, store.reviews[0].IncidentKey, store.reviews[1].IncidentKey)
	assert.Equal(t, store.reviews[0].IncidentKey, store.reviews[2].IncidentKey,
		"unrelated tool context must not create a second reputation incident")
}
