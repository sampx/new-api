package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ApplyThinkingConfig must translate the standard OpenAI Chat
// `reasoning_effort` field into Gemini `generationConfig.thinkingConfig`.
// Without this mapping the effort is silently dropped and every request falls
// back to the model's default thinking tier.
func TestOpenAIReasoningEffortToGeminiThinkingLevel(t *testing.T) {
	levelModels := map[string]string{
		"minimal": "minimal",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"max":     "xhigh",
	}

	for effort, wantLevel := range levelModels {
		t.Run("thinkingLevel dialect/"+effort, func(t *testing.T) {
			geminiRequest := &dto.GeminiChatRequest{}
			ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-v4-hydrogen"), dto.GeneralOpenAIRequest{
				Model:           "gemini-v4-hydrogen",
				ReasoningEffort: effort,
			})
			require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig, "thinkingConfig must be set for effort %q", effort)
			assert.Equal(t, wantLevel, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
			assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget,
				"thinkingLevel dialect must never send thinkingBudget")
		})
	}

	t.Run("thinkingLevel dialect/none cannot disable thinking", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-v4-hydrogen"), dto.GeneralOpenAIRequest{
			Model:           "gemini-v4-hydrogen",
			ReasoningEffort: "none",
		})
		assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig,
			"thinkingLevel models have no way to disable thinking; sending either field would 400")
	})

	t.Run("thinkingBudget dialect/effort becomes budget", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-2.5-flash"), dto.GeneralOpenAIRequest{
			Model:           "gemini-2.5-flash",
			ReasoningEffort: "high",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Equal(t, 24576*80/100, *geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Empty(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel,
			"thinkingBudget dialect must never send thinkingLevel")
	})

	t.Run("thinkingBudget dialect/none disables thinking", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-2.5-flash"), dto.GeneralOpenAIRequest{
			Model:           "gemini-2.5-flash",
			ReasoningEffort: "none",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Equal(t, 0, *geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
	})

	t.Run("thinkingBudget dialect/none respects model minimum budget", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-2.5-pro"), dto.GeneralOpenAIRequest{
			Model:           "gemini-2.5-pro",
			ReasoningEffort: "none",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Equal(t, 128, *geminiRequest.GenerationConfig.ThinkingConfig.ThinkingBudget,
			"gemini-2.5-pro cannot disable thinking and clamps to its 128-token minimum")
	})

	t.Run("unknown model version does not guess a dialect", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("my-gemini-alias"), dto.GeneralOpenAIRequest{
			Model:           "my-gemini-alias",
			ReasoningEffort: "high",
		})
		assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig,
			"without a known Gemini version the dialect is unknown; sending either field could 400")
	})

	t.Run("no effort keeps thinkingConfig empty", func(t *testing.T) {
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-v4-hydrogen"), dto.GeneralOpenAIRequest{
			Model: "gemini-v4-hydrogen",
		})
		assert.Nil(t, geminiRequest.GenerationConfig.ThinkingConfig)
	})

	t.Run("existing thinkingConfig from extra_body wins over reasoning_effort", func(t *testing.T) {
		// The caller already wrote the extra_body thinking_config; that explicit
		// provider-native setting must not be overwritten.
		geminiRequest := &dto.GeminiChatRequest{}
		geminiRequest.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			ThinkingLevel:   "high",
			IncludeThoughts: true,
		}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMeta("gemini-v4-hydrogen"), dto.GeneralOpenAIRequest{
			Model:           "gemini-v4-hydrogen",
			ReasoningEffort: "low",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	t.Run("effort suffix on model name wins over reasoning_effort", func(t *testing.T) {
		// -high etc. suffixes only reach the converter when the thinking adapter
		// is enabled (it strips the suffix in GetRequestURL); a model name that
		// carries one encodes the level directly.
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, reasoningEffortMetaWithAdapter("gemini-v4-hydrogen-high", true), dto.GeneralOpenAIRequest{
			Model:           "gemini-v4-hydrogen-high",
			ReasoningEffort: "low",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	t.Run("adapter flag does not gate the standard field", func(t *testing.T) {
		// ThinkingAdapterEnabled only governs new-api's own suffix conventions.
		// A standard Chat Completions reasoning_effort must map regardless.
		geminiRequest := &dto.GeminiChatRequest{}
		ApplyThinkingConfig(geminiRequest, &convmeta.Values{
			UpstreamModelName:   "gemini-v4-hydrogen",
			ChannelMetaAttached: true,
			Options:             &convmeta.Options{},
		}, dto.GeneralOpenAIRequest{
			Model:           "gemini-v4-hydrogen",
			ReasoningEffort: "high",
		})
		require.NotNil(t, geminiRequest.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	t.Run("meta records effective effort", func(t *testing.T) {
		meta := reasoningEffortMeta("gemini-v4-hydrogen")
		ApplyThinkingConfig(&dto.GeminiChatRequest{}, meta, dto.GeneralOpenAIRequest{
			Model:           "gemini-v4-hydrogen",
			ReasoningEffort: "high",
		})
		assert.Equal(t, "high", meta.GetReasoningEffort())
	})
}

func reasoningEffortMeta(model string) *convmeta.Values {
	return reasoningEffortMetaWithAdapter(model, false)
}

func reasoningEffortMetaWithAdapter(model string, adapter bool) *convmeta.Values {
	return &convmeta.Values{
		UpstreamModelName:   model,
		ChannelMetaAttached: true,
		Options: &convmeta.Options{
			Gemini: convmeta.GeminiOptions{
				ThinkingAdapterEnabled: adapter,
			},
		},
	}
}
