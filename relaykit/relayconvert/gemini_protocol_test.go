package relayconvert

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	sharedgemini "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/gemini"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiToOpenAIPartialArgsAndStableToolIndex(t *testing.T) {
	info := &convmeta.Values{
		ChannelMetaAttached: true,
		UpstreamModelName:   "gemini-4-hydrogen",
	}
	state, err := NewResponseStreamState(
		types.RelayFormatGemini,
		types.RelayFormatOpenAI,
		ResponseStreamOptions{ID: "chatcmpl-partial", Created: 1700000000},
	)
	require.NoError(t, err)

	strPtr := func(s string) *string { return &s }
	boolPtr := func(b bool) *bool { return &b }
	numPtr := func(n float64) *float64 { return &n }

	chunks := []*dto.GeminiChatResponse{
		{
			Candidates: []dto.GeminiChatCandidate{
				{
					Index: 0,
					Content: dto.GeminiChatContent{
						Role: "model",
						Parts: []dto.GeminiPart{
							{
								ThoughtSignature: []byte("\"real_sig_1\""),
								FunctionCall: &dto.FunctionCall{
									ID:           "fc_1",
									FunctionName: "read_file",
									WillContinue: boolPtr(true),
									PartialArgs: []dto.GeminiPartialArg{
										{JSONPath: "$.path", StringValue: strPtr("src/")},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			Candidates: []dto.GeminiChatCandidate{
				{
					Index: 0,
					Content: dto.GeminiChatContent{
						Role: "model",
						Parts: []dto.GeminiPart{
							{
								FunctionCall: &dto.FunctionCall{
									ID:           "fc_1",
									WillContinue: boolPtr(false),
									PartialArgs: []dto.GeminiPartialArg{
										{JSONPath: "$.path", StringValue: strPtr("main.go")},
										{JSONPath: "$.limit", NumberValue: numPtr(50)},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	r1, err := ConvertStreamResponseChunk(nil, info, state, chunks[0])
	require.NoError(t, err)
	assert.Empty(t, r1, "partial tool call chunk with willContinue=true must buffer until complete")

	r2, err := ConvertStreamResponseChunk(nil, info, state, chunks[1])
	require.NoError(t, err)
	require.Len(t, r2, 1)
	chatChunk, ok := r2[0].Value.(*dto.ChatCompletionsStreamResponse)
	require.True(t, ok)
	require.Len(t, chatChunk.Choices, 1)
	require.Len(t, chatChunk.Choices[0].Delta.ToolCalls, 1)
	tc := chatChunk.Choices[0].Delta.ToolCalls[0]
	assert.Equal(t, "fc_1", tc.ID)
	require.NotNil(t, tc.Index)
	assert.Equal(t, 0, *tc.Index)
	assert.Equal(t, "read_file", tc.Function.Name)
	assert.JSONEq(t, "{\"limit\":50,\"path\":\"src/main.go\"}", tc.Function.Arguments)
	require.NotNil(t, tc.ExtraContent)
	require.NotNil(t, tc.ExtraContent.Google)
	assert.Equal(t, "real_sig_1", tc.ExtraContent.Google.ThoughtSignature)

	// Antigravity tool-call stream ending cleanly without finishReason must emit tool_calls terminal chunk.
	finalResults, err := FinalizeStreamResponse(nil, info, state)
	require.NoError(t, err)
	require.Len(t, finalResults, 1)
	termChunk, ok := finalResults[0].Value.(*dto.ChatCompletionsStreamResponse)
	require.True(t, ok)
	require.Len(t, termChunk.Choices, 1)
	require.NotNil(t, termChunk.Choices[0].FinishReason)
	assert.Equal(t, types.FinishReasonToolCalls, *termChunk.Choices[0].FinishReason)
}

func TestGeminiToOpenAIStreamRejectsIncompletePartialAndZeroOutput(t *testing.T) {
	info := &convmeta.Values{
		ChannelMetaAttached: true,
		UpstreamModelName:   "gemini-4-hydrogen",
	}

	t.Run("incomplete partial function call at EOF errors", func(t *testing.T) {
		state, err := NewResponseStreamState(types.RelayFormatGemini, types.RelayFormatOpenAI, ResponseStreamOptions{ID: "c1", Created: 1})
		require.NoError(t, err)
		willContinue := true
		_, err = ConvertStreamResponseChunk(nil, info, state, &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{{
						FunctionCall: &dto.FunctionCall{ID: "fc_1", FunctionName: "read_file", WillContinue: &willContinue},
					}},
				},
			}},
		})
		require.NoError(t, err)
		_, err = FinalizeStreamResponse(nil, info, state)
		require.Error(t, err)
	})

	t.Run("pure thoughtSignature shell without visible output errors", func(t *testing.T) {
		state, err := NewResponseStreamState(types.RelayFormatGemini, types.RelayFormatOpenAI, ResponseStreamOptions{ID: "c2", Created: 1})
		require.NoError(t, err)
		results, err := ConvertStreamResponseChunk(nil, info, state, &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content: dto.GeminiChatContent{
					Role:  "model",
					Parts: []dto.GeminiPart{{Text: "", ThoughtSignature: []byte("\"opaque_sig\"")}},
				},
			}},
		})
		require.NoError(t, err)
		assert.Empty(t, results, "pure signature shell should not emit empty delta")
		_, err = FinalizeStreamResponse(nil, info, state)
		require.Error(t, err)
	})

	t.Run("malformed function call with zero visible output errors", func(t *testing.T) {
		state, err := NewResponseStreamState(types.RelayFormatGemini, types.RelayFormatOpenAI, ResponseStreamOptions{ID: "c3", Created: 1})
		require.NoError(t, err)
		reason := "MALFORMED_FUNCTION_CALL"
		_, err = ConvertStreamResponseChunk(nil, info, state, &dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				FinishReason: &reason,
				Content:      dto.GeminiChatContent{Role: "model"},
			}},
		})
		require.Error(t, err)
	})
}

func TestGeminiRequestPreservesIDsParallelSignatureAndRealSignature(t *testing.T) {
	info := &convmeta.Values{
		ChannelMetaAttached: true,
		UpstreamModelName:   "gemini-4-hydrogen",
		Options: &convmeta.Options{
			Gemini: convmeta.GeminiOptions{
				FunctionCallThoughtSignatureEnabled: true,
			},
		},
	}

	t.Run("chat completions preserves real signature and only tags first parallel call when missing", func(t *testing.T) {
		req := dto.GeneralOpenAIRequest{
			Model: "gemini-4-hydrogen",
			Messages: []dto.Message{
				{Role: "user", Content: "check two cities"},
				{
					Role: "assistant",
					ToolCalls: []byte(`[
						{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"A\"}"},"extra_content":{"google":{"thought_signature":"native_sig_abc"}}},
						{"id":"call_2","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"B\"}"}}
					]`),
				},
				{Role: "tool", ToolCallId: "call_1", Content: "{\"temp\":10}"},
				{Role: "tool", ToolCallId: "call_2", Content: "{\"temp\":20}"},
			},
		}

		gemReq, err := OpenAIChatRequestToGeminiGenerateContent(context.Background(), req, info)
		require.NoError(t, err)
		require.Len(t, gemReq.Contents, 3)

		modelParts := gemReq.Contents[1].Parts
		require.Len(t, modelParts, 2)
		assert.Equal(t, "call_1", modelParts[0].FunctionCall.ID)
		assert.Equal(t, "native_sig_abc", sharedgemini.ThoughtSignatureValue(modelParts[0]))
		assert.Equal(t, "call_2", modelParts[1].FunctionCall.ID)
		assert.Empty(t, modelParts[1].ThoughtSignature, "second parallel functionCall must not get a bypass signature")

		toolParts := gemReq.Contents[2].Parts
		require.Len(t, toolParts, 2)
		assert.Equal(t, "\"call_1\"", string(toolParts[0].FunctionResponse.ID))
		assert.Equal(t, "get_weather", toolParts[0].FunctionResponse.Name)
		assert.Equal(t, "\"call_2\"", string(toolParts[1].FunctionResponse.ID))
		assert.Equal(t, "get_weather", toolParts[1].FunctionResponse.Name)
	})

	t.Run("responses API passes IDs and attaches bypass only to first parallel call", func(t *testing.T) {
		req := dto.OpenAIResponsesRequest{
			Model: "gemini-4-hydrogen",
			Input: []byte(`[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"run"}]},
				{"type":"function_call","call_id":"resp_call_1","name":"tool_a","arguments":"{}"},
				{"type":"function_call","call_id":"resp_call_2","name":"tool_b","arguments":"{}"},
				{"type":"function_call_output","call_id":"resp_call_1","output":"ok1"},
				{"type":"function_call_output","call_id":"resp_call_2","output":"ok2"}
			]`),
		}

		result, err := ConvertRequest(context.Background(), info, types.RelayFormatGemini, &req)
		require.NoError(t, err)
		gemReq, ok := result.Value.(*dto.GeminiChatRequest)
		require.True(t, ok)
		require.Len(t, gemReq.Contents, 3)

		modelParts := gemReq.Contents[1].Parts
		require.Len(t, modelParts, 2)
		assert.Equal(t, "resp_call_1", modelParts[0].FunctionCall.ID)
		assert.Equal(t, sharedgemini.ThoughtSignatureBypassValue, sharedgemini.ThoughtSignatureValue(modelParts[0]))
		assert.Equal(t, "resp_call_2", modelParts[1].FunctionCall.ID)
		assert.Empty(t, modelParts[1].ThoughtSignature)

		respParts := gemReq.Contents[2].Parts
		require.Len(t, respParts, 2)
		assert.Equal(t, "\"resp_call_1\"", string(respParts[0].FunctionResponse.ID))
		assert.Equal(t, "tool_a", respParts[0].FunctionResponse.Name)
		assert.Equal(t, "\"resp_call_2\"", string(respParts[1].FunctionResponse.ID))
		assert.Equal(t, "tool_b", respParts[1].FunctionResponse.Name)
	})
}

func TestGeminiCleanFunctionParametersDropsNonStringEnum(t *testing.T) {
	cleaned := sharedgemini.CleanFunctionParameters(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"flag": map[string]any{
				"type": "boolean",
				"enum": []any{true},
			},
			"mode": map[string]any{
				"type": "string",
				"enum": []any{"fast", "slow"},
			},
		},
	}).(map[string]any)

	props := cleaned["properties"].(map[string]any)
	flagSchema := props["flag"].(map[string]any)
	assert.Equal(t, "BOOLEAN", flagSchema["type"])
	_, hasFlagEnum := flagSchema["enum"]
	assert.False(t, hasFlagEnum)

	modeSchema := props["mode"].(map[string]any)
	assert.Equal(t, "STRING", modeSchema["type"])
	assert.Equal(t, []any{"fast", "slow"}, modeSchema["enum"])
}
