package service

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildChannelAffinityStatsContextForTest(t *testing.T, ruleName, usingGroup, keyFP string) *gin.Context {
	t.Helper()
	cache := getChannelAffinityUsageCacheStatsCache()
	keys := []string{channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFP)}
	_, err := cache.DeleteMany(keys)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := cache.DeleteMany(keys)
		require.NoError(t, err)
	})

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	setChannelAffinityContext(ctx, channelAffinityMeta{
		CacheKey:       fmt.Sprintf("test:%s:%s:%s", ruleName, usingGroup, keyFP),
		TTLSeconds:     600,
		RuleName:       ruleName,
		UsingGroup:     usingGroup,
		KeyFingerprint: keyFP,
	})
	return ctx
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_ClaudeMode(t *testing.T) {
	ruleName := t.Name()
	usingGroup := "default"
	keyFP := "test-key"
	ctx := buildChannelAffinityStatsContextForTest(t, ruleName, usingGroup, keyFP)

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, types.RelayFormatClaude)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	assert.EqualValues(t, 1, stats.Total)
	assert.EqualValues(t, 1, stats.Hit)
	assert.EqualValues(t, 100, stats.PromptTokens)
	assert.EqualValues(t, 40, stats.CompletionTokens)
	assert.EqualValues(t, 140, stats.TotalTokens)
	assert.EqualValues(t, 30, stats.CachedTokens)
	assert.Equal(t, cacheTokenRateModeCachedOverPromptPlusCached, stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_MixedMode(t *testing.T) {
	ruleName := t.Name()
	usingGroup := "default"
	keyFP := "test-key"
	ctx := buildChannelAffinityStatsContextForTest(t, ruleName, usingGroup, keyFP)

	openAIUsage := &dto.Usage{
		PromptTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 10,
		},
	}
	claudeUsage := &dto.Usage{
		PromptTokens: 80,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 20,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, openAIUsage, types.RelayFormatOpenAI)
	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, claudeUsage, types.RelayFormatClaude)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	assert.EqualValues(t, 2, stats.Total)
	assert.EqualValues(t, 2, stats.Hit)
	assert.EqualValues(t, 180, stats.PromptTokens)
	assert.EqualValues(t, 30, stats.CachedTokens)
	assert.Equal(t, cacheTokenRateModeMixed, stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_UnsupportedModeKeepsEmpty(t *testing.T) {
	ruleName := t.Name()
	usingGroup := "default"
	keyFP := "test-key"
	ctx := buildChannelAffinityStatsContextForTest(t, ruleName, usingGroup, keyFP)

	usage := &dto.Usage{
		PromptTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 25,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, types.RelayFormatGemini)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	assert.EqualValues(t, 1, stats.Total)
	assert.EqualValues(t, 1, stats.Hit)
	assert.EqualValues(t, 25, stats.CachedTokens)
	assert.Equal(t, "", stats.CachedTokenRateMode)
}
