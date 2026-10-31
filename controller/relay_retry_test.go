package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayRetryClientMessageKeepsAllAttemptsWithin400Runes(t *testing.T) {
	attempts := make([]relayAttemptError, 0, 9)
	for i := 0; i < 9; i++ {
		attempts = append(attempts, relayAttemptError{
			ChannelID:   11 + i,
			ChannelName: "very long upstream channel name",
			StatusCode:  http.StatusServiceUnavailable,
			Message:     strings.Repeat("上游错误详情很长", 30),
		})
	}

	message := relayRetryClientMessage(attempts, "req-1234567890")

	require.LessOrEqual(t, utf8.RuneCountInString(message), maxRelayRetryClientMessageRunes)
	for i := 0; i < 9; i++ {
		assert.Contains(t, message, "#"+strconv.Itoa(11+i)+"/503")
	}
	assert.Contains(t, message, "request id: req-1234567890")
}

func TestFormatRelayRetrySummaryUsesFullReadableErrorsWhenTheyFit(t *testing.T) {
	attempts := []relayAttemptError{
		{ChannelID: 11, ChannelName: "ModelScope", StatusCode: http.StatusBadRequest, Message: "developer role unsupported"},
		{ChannelID: 23, ChannelName: "Local DeepSeek", StatusCode: http.StatusServiceUnavailable, Message: "no available account"},
	}

	summary := formatRelayRetrySummary(attempts, maxRelayRetryClientMessageRunes)

	assert.Equal(t, "#11 ModelScope/400:developer role unsupported; #23 Local DeepSeek/503:no available account", summary)
}

func TestRelayRetryClientMessageCompactsWhitespaceAndOmitsHistoryWhenEmpty(t *testing.T) {
	attempts := []relayAttemptError{
		{ChannelID: 11, StatusCode: http.StatusBadGateway, Message: " upstream\n  failed\tbadly "},
	}

	message := relayRetryClientMessage(attempts, "req-1")

	assert.Contains(t, message, "#11/502:upstream failed badly")
	assert.NotContains(t, message, "\n")
	assert.Empty(t, relayRetryClientMessage(nil, "req-1"))
}

func TestShouldReturnRelayRetrySummaryOnlyForTerminalRetryFailure(t *testing.T) {
	upstreamErr := types.NewErrorWithStatusCode(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)
	otherErr := types.NewError(
		errors.New("local relay failure"),
		types.ErrorCodeReadRequestBodyFailed,
	)
	attempts := []relayAttemptError{{apiError: upstreamErr}}

	assert.False(t, shouldReturnRelayRetrySummary(attempts, 0, upstreamErr), "a single initial failure should keep the normal error response")
	assert.True(t, shouldReturnRelayRetrySummary(attempts, 1, upstreamErr), "a terminal failure after retry should expose the compact retry summary")
	assert.False(t, shouldReturnRelayRetrySummary(attempts, 1, otherErr), "a later local failure must not be replaced by historical upstream errors")
	assert.False(t, shouldReturnRelayRetrySummary(attempts, 1, nil), "a successful retry must not expose historical upstream errors")
}

func TestShouldRetryRespectsConfiguredRetryBudgetAndStatusCodes(t *testing.T) {
	originalRanges := operation_setting.AutomaticRetryStatusCodeRanges
	t.Cleanup(func() {
		operation_setting.AutomaticRetryStatusCodeRanges = originalRanges
	})
	operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{
		{Start: http.StatusServiceUnavailable, End: http.StatusServiceUnavailable},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	upstreamErr := types.NewErrorWithStatusCode(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)

	require.True(t, shouldRetry(c, upstreamErr, 8),
		"a configured retryable upstream error should retry while retry budget remains")
	require.False(t, shouldRetry(c, upstreamErr, 0),
		"retryable upstream errors must stop when the configured retry budget is exhausted")

	nonRetryableErr := types.NewErrorWithStatusCode(
		errors.New("bad request"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusBadRequest,
	)
	require.False(t, shouldRetry(c, nonRetryableErr, 8),
		"channel exhaustion must not bypass the configured retry status-code policy")
}

func TestFinalRelayErrorAfterSelectionFailurePreservesLastUpstreamError(t *testing.T) {
	lastUpstreamErr := types.WithOpenAIError(types.OpenAIError{
		Message: "real upstream failure",
		Type:    "upstream_error",
		Code:    "upstream_failure",
	}, http.StatusServiceUnavailable)
	selectionErr := types.NewError(
		errors.New("all channels and keys in this group have been exhausted"),
		types.ErrorCodeGetChannelFailed,
	)
	relayInfo := &common.RelayInfo{LastError: lastUpstreamErr}
	retryParam := &service.RetryParam{
		ExcludeKeys:        map[string]bool{"23_0": true},
		SelectionExhausted: true,
	}

	actual := finalRelayErrorAfterSelectionFailure(selectionErr, relayInfo, retryParam)

	assert.Same(t, lastUpstreamErr, actual)
}

func TestFinalRelayErrorAfterSelectionFailureKeepsInitialSelectionError(t *testing.T) {
	selectionErr := types.NewError(
		errors.New("no available channel"),
		types.ErrorCodeGetChannelFailed,
	)

	actual := finalRelayErrorAfterSelectionFailure(
		selectionErr,
		&common.RelayInfo{},
		&service.RetryParam{ExcludeKeys: map[string]bool{}},
	)

	assert.Same(t, selectionErr, actual)
}

func TestFinalRelayErrorAfterSelectionFailureKeepsNonSelectionError(t *testing.T) {
	lastUpstreamErr := types.WithOpenAIError(types.OpenAIError{
		Message: "real upstream failure",
		Type:    "upstream_error",
		Code:    "upstream_failure",
	}, http.StatusServiceUnavailable)
	channelErr := types.NewError(
		errors.New("selected channel setup failed"),
		types.ErrorCodeChannelNoAvailableKey,
	)

	actual := finalRelayErrorAfterSelectionFailure(
		channelErr,
		&common.RelayInfo{LastError: lastUpstreamErr},
		&service.RetryParam{ExcludeKeys: map[string]bool{"23_0": true}},
	)

	assert.Same(t, channelErr, actual)
}

func TestFinalRelayErrorAfterSelectionFailureKeepsNonExhaustionSelectionError(t *testing.T) {
	lastUpstreamErr := types.WithOpenAIError(types.OpenAIError{
		Message: "real upstream failure",
		Type:    "upstream_error",
		Code:    "upstream_failure",
	}, http.StatusServiceUnavailable)
	selectionErr := types.NewError(
		errors.New("channel selection storage failed"),
		types.ErrorCodeGetChannelFailed,
	)

	actual := finalRelayErrorAfterSelectionFailure(
		selectionErr,
		&common.RelayInfo{LastError: lastUpstreamErr},
		&service.RetryParam{
			ExcludeKeys:        map[string]bool{"23_0": true},
			SelectionExhausted: false,
		},
	)

	assert.Same(t, selectionErr, actual)
}
