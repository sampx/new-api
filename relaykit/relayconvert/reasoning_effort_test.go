package relayconvert

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The standard OpenAI Chat reasoning_effort field must reach the Gemini
// payload through the public conversion entry point, with or without the
// new-api thinking adapter enabled. This is the contract the proxy chain
// (client -> new-api -> Gemini) depends on.
func TestOpenAIChatReasoningEffortReachesGeminiPayload(t *testing.T) {
	convert := func(t *testing.T, model string, adapter bool, effort string) *dto.GeminiChatRequest {
		t.Helper()
		got, err := OpenAIChatRequestToGeminiGenerateContent(context.Background(), dto.GeneralOpenAIRequest{
			Model:           model,
			ReasoningEffort: effort,
			Messages: []dto.Message{
				{Role: "user", Content: "hello"},
			},
		}, &convmeta.Values{
			UpstreamModelName:   model,
			ChannelMetaAttached: true,
			Options: &convmeta.Options{
				Gemini: convmeta.GeminiOptions{ThinkingAdapterEnabled: adapter},
			},
		})
		require.NoError(t, err)
		return got
	}

	t.Run("thinkingLevel model with adapter disabled", func(t *testing.T) {
		got := convert(t, "gemini-v4-hydrogen", false, "xhigh")
		body, err := kitutil.Marshal(got)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"thinkingLevel":"xhigh"`)
		assert.NotContains(t, string(body), `"thinkingBudget"`)
	})

	t.Run("thinkingBudget model keeps the budget dialect", func(t *testing.T) {
		got := convert(t, "gemini-2.5-flash", false, "low")
		body, err := kitutil.Marshal(got)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"thinkingBudget"`)
		assert.NotContains(t, string(body), `"thinkingLevel"`)
	})

	t.Run("no effort leaves the payload untouched", func(t *testing.T) {
		got := convert(t, "gemini-v4-hydrogen", false, "")
		body, err := kitutil.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(body), `"thinkingConfig"`)
	})

	t.Run("explicit extra_body thinking_config wins", func(t *testing.T) {
		got, err := OpenAIChatRequestToGeminiGenerateContent(context.Background(), dto.GeneralOpenAIRequest{
			Model:           "gemini-v4-hydrogen",
			ReasoningEffort: "low",
			ExtraBody:       []byte(`{"google":{"thinking_config":{"thinking_level":"high"}}}`),
			Messages: []dto.Message{
				{Role: "user", Content: "hello"},
			},
		}, &convmeta.Values{
			UpstreamModelName:   "gemini-v4-hydrogen",
			ChannelMetaAttached: true,
			Options:             &convmeta.Options{},
		})
		require.NoError(t, err)
		require.NotNil(t, got.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", got.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})
}
