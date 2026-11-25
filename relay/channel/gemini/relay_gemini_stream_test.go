package gemini

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiChatStreamHandlerPartialArgsAndEOFWithoutFinishReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	info := &relaycommon.RelayInfo{
		IsStream:        true,
		RelayFormat:     types.RelayFormatOpenAI,
		DisablePing:     true,
		OriginModelName: "gemini-4-hydrogen",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-4-hydrogen",
		},
	}

	willContinueTrue := true
	willContinueFalse := false
	part1 := "read_"
	part2 := "file.go"
	chunk1, err := common.Marshal(dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content: dto.GeminiChatContent{
				Role: "model",
				Parts: []dto.GeminiPart{{
					ThoughtSignature: []byte("\"sig_xyz\""),
					FunctionCall: &dto.FunctionCall{
						ID:           "fc_stream_1",
						FunctionName: "read_file",
						WillContinue: &willContinueTrue,
						PartialArgs:  []dto.GeminiPartialArg{{JSONPath: "$.path", StringValue: &part1}},
					},
				}},
			},
		}},
	})
	require.NoError(t, err)

	chunk2, err := common.Marshal(dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content: dto.GeminiChatContent{
				Role: "model",
				Parts: []dto.GeminiPart{{
					FunctionCall: &dto.FunctionCall{
						ID:           "fc_stream_1",
						WillContinue: &willContinueFalse,
						PartialArgs:  []dto.GeminiPartialArg{{JSONPath: "$.path", StringValue: &part2}},
					},
				}},
			},
		}},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 5,
			TotalTokenCount:      15,
		},
	})
	require.NoError(t, err)

	// No finishReason and no [DONE] (matches Antigravity EOF tool stream behavior).
	streamBody := "data: " + string(chunk1) + "\n\n" + "data: " + string(chunk2) + "\n\n"
	usage, newAPIError := GeminiChatStreamHandler(c, info, &http.Response{
		Body: io.NopCloser(strings.NewReader(streamBody)),
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 15, usage.TotalTokens)

	got := recorder.Body.String()
	assert.Contains(t, got, `"id":"fc_stream_1"`)
	assert.Contains(t, got, `"name":"read_file"`)
	assert.Contains(t, got, "read_file.go")
	assert.Contains(t, got, `"thought_signature":"sig_xyz"`)
	assert.Contains(t, got, `"finish_reason":"tool_calls"`)
	assert.Contains(t, got, "data: [DONE]")
}

func TestGeminiChatHandlersRejectPureSignatureZeroOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("stream pure signature shell returns empty response error before writing body", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		oldStreamingTimeout := constant.StreamingTimeout
		constant.StreamingTimeout = 300
		t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

		info := &relaycommon.RelayInfo{
			IsStream:        true,
			RelayFormat:     types.RelayFormatOpenAI,
			DisablePing:     true,
			OriginModelName: "gemini-4-hydrogen",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gemini-4-hydrogen"},
		}
		shellChunk, err := common.Marshal(dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content: dto.GeminiChatContent{
					Role:  "model",
					Parts: []dto.GeminiPart{{Text: "", ThoughtSignature: []byte("\"opaque_only\"")}},
				},
			}},
		})
		require.NoError(t, err)

		_, newAPIError := GeminiChatStreamHandler(c, info, &http.Response{
			Body: io.NopCloser(strings.NewReader("data: " + string(shellChunk) + "\n\ndata: [DONE]\n")),
		})
		require.NotNil(t, newAPIError)
		assert.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
		assert.Equal(t, types.ErrorCodeEmptyResponse, newAPIError.GetErrorCode())
		assert.Empty(t, recorder.Body.String())
		assert.Empty(t, recorder.Header().Get("Transfer-Encoding"))
	})

	t.Run("non-stream pure signature candidate returns empty response error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

		info := &relaycommon.RelayInfo{
			RelayFormat:     types.RelayFormatOpenAI,
			OriginModelName: "gemini-4-hydrogen",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gemini-4-hydrogen"},
		}
		payload, err := common.Marshal(dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content: dto.GeminiChatContent{
					Role:  "model",
					Parts: []dto.GeminiPart{{Text: "", ThoughtSignature: []byte("\"opaque_only\"")}},
				},
			}},
		})
		require.NoError(t, err)

		_, newAPIError := GeminiChatHandler(c, info, &http.Response{
			Body: io.NopCloser(bytes.NewReader(payload)),
		})
		require.NotNil(t, newAPIError)
		assert.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
		assert.Equal(t, types.ErrorCodeEmptyResponse, newAPIError.GetErrorCode())
	})

	t.Run("responses stream pure signature shell returns empty response error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Set(common.RequestIdKey, "resp-empty-shell")

		oldStreamingTimeout := constant.StreamingTimeout
		constant.StreamingTimeout = 300
		t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

		info := &relaycommon.RelayInfo{
			IsStream:        true,
			RelayMode:       relayconstant.RelayModeResponses,
			RelayFormat:     types.RelayFormatOpenAIResponses,
			DisablePing:     true,
			OriginModelName: "gemini-4-hydrogen",
			ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: "gemini-4-hydrogen"},
		}
		shellChunk, err := common.Marshal(dto.GeminiChatResponse{
			Candidates: []dto.GeminiChatCandidate{{
				Content: dto.GeminiChatContent{
					Role:  "model",
					Parts: []dto.GeminiPart{{Text: "", ThoughtSignature: []byte("\"opaque_only\"")}},
				},
			}},
		})
		require.NoError(t, err)

		_, newAPIError := GeminiResponsesStreamHandler(c, info, &http.Response{
			Body: io.NopCloser(strings.NewReader("data: " + string(shellChunk) + "\n\ndata: [DONE]\n")),
		})
		require.NotNil(t, newAPIError)
		assert.Equal(t, http.StatusBadGateway, newAPIError.StatusCode)
		assert.Equal(t, types.ErrorCodeEmptyResponse, newAPIError.GetErrorCode())
		assert.Empty(t, recorder.Body.String())
	})
}
