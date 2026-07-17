package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/relay/stream"
	openaiOutbound "github.com/bestruirui/octopus/internal/transformer/outbound/openai"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// forwardViaWS attempts to forward via upstream WebSocket.
// Returns statusCode=-1 if WS is not available (caller should fall through to HTTP).
func (ra *relayAttempt) forwardViaWS(ctx context.Context) (int, error) {
	if ra.c == nil && effectiveResponsesWSMode(ra.channel) == responsesWSModePassthrough && !ra.internalRequest.IsOpenAIExactReplayRequest() {
		return ra.forwardViaWSPassthrough(ctx)
	}
	continuation := requiresUpstreamWSContinuation(ra.internalRequest)
	pc := ra.openTransformWS(ctx, continuation)
	if pc == nil {
		log.Debugf("upstream WS unavailable for channel %s (key=%d, continuation=%t)", ra.channel.Name, ra.usedKey.ID, continuation)
		return -1, nil
	}
	log.Debugf("using upstream WebSocket for channel %s (key=%d)", ra.channel.Name, ra.usedKey.ID)
	log.Debugf("upstream WS selected (channel=%s, key=%d, continuation=%t, previous_response_id=%s)",
		ra.channel.Name, ra.usedKey.ID, continuation, currentPreviousResponseID(ra.internalRequest))
	responsesReq := openaiOutbound.ConvertToResponsesRequest(ra.internalRequest)
	reqBody, err := json.Marshal(responsesReq)
	if err != nil {
		wsUpstreamPool.Put(pc)
		return -1, nil
	}
	ra.metrics.SetTransportRequestPayload(reqBody, ra.internalRequest.Model)
	if err := wsUpstreamPool.SendResponseCreate(ctx, pc, reqBody); err != nil {
		return ra.handleWSSendFailure(ctx, pc, reqBody, continuation, err)
	}
	ra.metrics.UsedWS = true
	ra.metrics.SetWSExecMode(dbmodel.RelayLogWSExecModeTransform)
	if ra.metrics.WSMode == nil {
		ra.metrics.SetWSMode(defaultWSModeForRequest(ra.internalRequest))
	}
	reader := newWSUpstreamReader(pc, ra.channel.ID, ra.usedKey.ID)
	err = ra.handleWSStreamResponseV2(ctx, reader)
	if err != nil {
		return ra.handleWSStreamFailure(ctx, reader, reqBody, continuation, err)
	}
	reader.Close()
	wsUpstreamPool.RecordWSSuccess(ra.channel.ID)
	ra.recordSuccessfulWSAffinity(pc)
	return 200, nil
}

func (ra *relayAttempt) openTransformWS(ctx context.Context, continuation bool) *pooledConn {
	preferredConnID := ""
	if continuation {
		preferredConnID, _ = getWSResponseConn(currentPreviousResponseID(ra.internalRequest))
	}
	return TryUpstreamWSWithPreference(ctx, ra.channel, ra.effectiveBaseURL(), ra.usedKey.ChannelKey,
		ra.usedKey.ID, ra.clientRequestHeaders(), preferredConnID)
}

func (ra *relayAttempt) handleWSSendFailure(ctx context.Context, pc *pooledConn, reqBody []byte, continuation bool, sendErr error) (int, error) {
	log.Warnf("upstream WS send failed for channel %s: %v", ra.channel.Name, sendErr)
	log.Debugf("upstream WS send failed before stream start (channel=%s, key=%d, continuation=%t, err=%v)",
		ra.channel.Name, ra.usedKey.ID, continuation, sendErr)
	wsUpstreamPool.RemoveConn(pc)
	if isUpstreamWSConnectionBroken(sendErr) {
		log.Debugf("upstream WS send failure eligible for redial (channel=%s, key=%d, continuation=%t)",
			ra.channel.Name, ra.usedKey.ID, continuation)
		statusCode, redialErr, recovered := ra.retryViaFreshUpstreamWS(ctx, reqBody)
		if recovered || redialErr != nil {
			return statusCode, redialErr
		}
		if continuation {
			balancer.DeleteSticky(ra.apiKeyID, ra.requestModel)
			return http.StatusConflict, fmt.Errorf("upstream continuation transport unavailable; please restart the conversation")
		}
	}
	wsUpstreamPool.RecordWSFailure(ra.channel.ID)
	return -1, nil
}

func (ra *relayAttempt) handleWSStreamFailure(ctx context.Context, reader *wsUpstreamReader, reqBody []byte, continuation bool, streamErr error) (int, error) {
	reader.CloseWithError()
	log.Debugf("upstream WS stream failed (channel=%s, key=%d, continuation=%t, written=%t, status=%d, err=%v)",
		ra.channel.Name, ra.usedKey.ID, continuation, ra.getStreamWriter().Written(), reader.StatusCode(), streamErr)
	if continuation && !ra.streamPayloadWritten.Load() && shouldReconnectUpstreamWSBeforeReplay(streamErr) {
		log.Debugf("upstream WS stream failure eligible for reconnect before replay (channel=%s, key=%d, previous_response_id=%s)",
			ra.channel.Name, ra.usedKey.ID, currentPreviousResponseID(ra.internalRequest))
		statusCode, redialErr, recovered := ra.retryViaFreshUpstreamWS(ctx, reqBody)
		if recovered || redialErr != nil {
			return statusCode, redialErr
		}
	}
	if continuation && isContinuationTransportFailure(streamErr) {
		balancer.DeleteSticky(ra.apiKeyID, ra.requestModel)
		return http.StatusConflict, fmt.Errorf("upstream continuation transport unavailable; please restart the conversation")
	}
	if ra.requestContext().Err() == nil {
		wsUpstreamPool.RecordWSFailure(ra.channel.ID)
	}
	return reader.StatusCode(), streamErr
}

func (ra *relayAttempt) retryViaFreshUpstreamWS(ctx context.Context, reqBody []byte) (int, error, bool) {
	log.Debugf("attempting fresh upstream WS redial (channel=%s, key=%d, previous_response_id=%s)",
		ra.channel.Name, ra.usedKey.ID, currentPreviousResponseID(ra.internalRequest))
	redialed := TryUpstreamWS(ctx, ra.channel, ra.effectiveBaseURL(), ra.usedKey.ChannelKey, ra.usedKey.ID, ra.clientRequestHeaders(), true)
	if redialed == nil {
		log.Debugf("fresh upstream WS redial unavailable (channel=%s, key=%d)", ra.channel.Name, ra.usedKey.ID)
		return 0, nil, false
	}

	retryErr := wsUpstreamPool.SendResponseCreate(ctx, redialed, reqBody)
	if retryErr != nil {
		log.Warnf("upstream WS redial send failed for channel %s: %v", ra.channel.Name, retryErr)
		log.Debugf("fresh upstream WS redial send failed (channel=%s, key=%d, err=%v)", ra.channel.Name, ra.usedKey.ID, retryErr)
		wsUpstreamPool.RemoveConn(redialed)
		wsUpstreamPool.RecordWSFailure(ra.channel.ID)
		if requiresUpstreamWSContinuation(ra.internalRequest) {
			balancer.DeleteSticky(ra.apiKeyID, ra.requestModel)
			return http.StatusConflict, fmt.Errorf("upstream continuation transport unavailable; please restart the conversation"), true
		}
		return -1, nil, true
	}

	ra.metrics.UsedWS = true
	ra.metrics.SetWSExecMode(dbmodel.RelayLogWSExecModeTransform)
	if ra.metrics.WSMode == nil {
		ra.metrics.SetWSMode(defaultWSModeForRequest(ra.internalRequest))
	}
	ra.metrics.SetWSRecovery(dbmodel.RelayLogWSRecoveryReconnect)
	reader := newWSUpstreamReader(redialed, ra.channel.ID, ra.usedKey.ID)
	streamErr := ra.handleWSStreamResponseV2(ctx, reader)
	if streamErr != nil {
		reader.CloseWithError()
		log.Debugf("fresh upstream WS redial stream failed (channel=%s, key=%d, status=%d, err=%v)",
			ra.channel.Name, ra.usedKey.ID, reader.StatusCode(), streamErr)
		if requiresUpstreamWSContinuation(ra.internalRequest) && isContinuationTransportFailure(streamErr) {
			balancer.DeleteSticky(ra.apiKeyID, ra.requestModel)
			return http.StatusConflict, fmt.Errorf("upstream continuation transport unavailable; please restart the conversation"), true
		}
		if ra.requestContext().Err() == nil {
			wsUpstreamPool.RecordWSFailure(ra.channel.ID)
		}
		return reader.StatusCode(), streamErr, true
	}
	log.Debugf("fresh upstream WS redial succeeded (channel=%s, key=%d, previous_response_id=%s)",
		ra.channel.Name, ra.usedKey.ID, currentPreviousResponseID(ra.internalRequest))
	reader.Close()
	wsUpstreamPool.RecordWSSuccess(ra.channel.ID)
	ra.recordSuccessfulWSAffinity(redialed)
	return http.StatusOK, nil, true
}

func isContinuationTransportFailure(err error) bool {
	// Check for empty stream error (both old message and new error type)
	if errors.Is(err, stream.ErrEmptyUpstreamStream) {
		return true
	}
	message := relayErrorMessage(err)
	return isUpstreamWSConnectionBroken(err) ||
		needsConversationRestart(message) ||
		strings.Contains(message, "ws stream ended before first event")
}

func (ra *relayAttempt) clientRequestHeaders() http.Header {
	if ra == nil || ra.c == nil || ra.c.Request == nil {
		return nil
	}
	return ra.c.Request.Header
}

func (ra *relayAttempt) handleWSStreamResponseV2(ctx context.Context, reader *wsUpstreamReader) error {
	defer ra.closeFirstTokenBudget()

	// Hand off early heartbeat
	ra.heartbeat.Hand()

	// Build transform function
	transform := func(ctx context.Context, data []byte) ([]byte, error) {
		return ra.transformStreamData(ctx, string(data))
	}

	// Determine first token timeout
	var firstTokenTimeout time.Duration
	if ra.firstTokenTimeOutSec > 0 && ra.firstTokenBudget == nil {
		firstTokenTimeout = time.Duration(ra.firstTokenTimeOutSec) * time.Second
	}

	// Create StreamProcessor
	processor := stream.NewStreamProcessor(stream.StreamConfig{
		Source:            stream.NewWSSource(reader),
		Transform:         transform,
		Writer:            ra.getStreamWriter(),
		Context:           ctx,
		FirstTokenTimeout: firstTokenTimeout,
		HeartbeatInterval: streamHeartbeatInterval(),
		OnFirstToken: func() {
			ra.metrics.SetFirstTokenTime(time.Now())
			ra.stopFirstTokenTimer()
		},
	})

	// Run processor
	err := processor.Run()

	// Track payload written for metrics collection
	if processor.PayloadWritten() {
		ra.streamPayloadWritten.Store(true)
	}

	// Handle first token timeout specifically
	if err != nil && strings.Contains(err.Error(), "first token timeout") {
		return ra.firstTokenTimeoutError()
	}

	// Check for context cancellation with first token timeout
	if err != nil {
		if timeoutErr := ra.firstTokenTimeoutIfNeeded(ctx, err); timeoutErr != nil {
			return timeoutErr
		}
	}

	return err
}
