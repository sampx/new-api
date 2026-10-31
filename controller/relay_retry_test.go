package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
