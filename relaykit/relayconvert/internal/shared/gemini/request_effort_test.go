package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ApplyThinkingConfig must translate the standard OpenAI Chat
// `reasoning_effort` field into Gemini `generationConfig.thinkingConfig`
// when the request reaches a Gemini channel without any explicit
// provider-native thinking_config. Without this mapping the effort is
// silently dropped and every request falls back to the model's lowest
// thinking tier.
func TestOpenAIReasoningEffortToGeminiThinkingLevel(t *testing.T) {
	validLevels := map[string]struct {
		effort string
		level  string
		budget *int
	}{
		"minimal": {effort: "minimal", level: "minimal"},
		"low":     {effort: "low", level: "low"},
		"medium":  {effort: "medium", level: "medium"},
		"high":    {effort: "high", level: "high"},
		"xhigh":   {effort: "xhigh", level: "xhigh"},
		"max":     {effort: "max", level: "xhigh"},
		"none":    {effort: "none", level: "", budget: ptr(0)},
	}

	for name, tc := range validLevels {
		t.Run(name, func(t *testing.T) {
			geminiRequest := &dto.GeminiChatRequest{}
			ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-test"), dto.GeneralOpenAIRequest{
				Model:           "gemini-test",
				ReasoningEffort: tc.effort,
			})
			require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig, "thinkingConfig must be set for effort %q", tc.effort)
			assert.Equal(t, tc.level, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
			if tc.budget != nil {
				require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
				assert.Equal(t, *tc.budget, *geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
			} else {
				assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
			}
		})
	}

	t.Run("no effort keeps thinkingConfig empty", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-test"), dto.GeneralOpenAIRequest{
			Model: "gemini-test",
		})
		assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig)
	})

	t.Run("existing thinkingConfig from extra_body wins over reasoning_effort", func(t *testing.T) {
		// The caller (to_gemini_chat_req.go) already wrote the extra_body
		// thinking_config before invoking the adapter; that explicit
		// provider-native setting must not be overwritten.
		geminiRequest := &dto.GeminiChatRequest{}
		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			ThinkingLevel:   "high",
			IncludeThoughts: true,
		}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-test"), dto.GeneralOpenAIRequest{
			Model:           "gemini-test",
			ReasoningEffort: "low",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	t.Run("effort suffix on model name wins over reasoning_effort", func(t *testing.T) {
		// -high etc. suffixes only reach the converter when
		// ThinkingAdapterEnabled strips them in GetRequestURL; a model name
		// that carries one encodes the level directly, so the request effort
		// must not override it.
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-test-high"), dto.GeneralOpenAIRequest{
			Model:           "gemini-test-high",
			ReasoningEffort: "low",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	t.Run("adapter disabled keeps thinkingConfig empty", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, &convmeta.Values{}, dto.GeneralOpenAIRequest{
			Model:           "gemini-test",
			ReasoningEffort: "high",
		})
		assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig)
	})

	t.Run("meta records effective effort", func(t *testing.T) {
		meta := reasoningEffortMeta("gemini-test")
		ApplyThinkingConfig(&dto.GeminiChatRequest{}, meta, dto.GeneralOpenAIRequest{
			Model:           "gemini-test",
			ReasoningEffort: "high",
		})
		assert.Equal(t, "high", meta.GetReasoningEffort())
	})
}

func reasoningEffortMeta(model string) *convmeta.Values {
	return &convmeta.Values{
		UpstreamModelName:   model,
		ChannelMetaAttached: true,
		Options: &convmeta.Options{
			Gemini: convmeta.GeminiOptions{
				ThinkingAdapterEnabled: true,
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }
