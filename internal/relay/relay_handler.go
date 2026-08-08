package relay

import (
	"context"
	"fmt"
	"net/http"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/outlierwindow"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/relay/compress"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

func Handler(inboundType inbound.InboundType, c *gin.Context) {
	handler, ok := newRelayHandler(inboundType, c)
	if !ok {
		return
	}
	defer handler.heartbeat.Stop()
	handler.run()
}

type relayHandler struct {
	inboundType            inbound.InboundType
	c                      *gin.Context
	group                  dbmodel.Group
	protocolRoutingEnabled bool
	replayState            *wsConversationState
	iterator               *balancer.Iterator
	heartbeat              *earlyHeartbeat
	metrics                *RelayMetrics
	request                *relayRequest
	passthroughRequired    bool
	passthroughCapable     bool
	lastErr                error
	lastResult             attemptResult
	capacitySkipped        bool
	rateSkipped            bool
	maxRetries             int
}

func newRelayHandler(inboundType inbound.InboundType, c *gin.Context) (*relayHandler, bool) {
	rawBody, request, inAdapter, err := parseRequest(inboundType, c)
	if err != nil || !validateSupportedModel(c, request.Model) {
		return nil, false
	}
	group, err := op.GroupGetEnabledMap(request.Model, c.Request.Context())
	if err != nil {
		resp.ErrorWithCode(c, http.StatusNotFound, CodeRelayModelNotFound, "model not found")
		return nil, false
	}
	// 请求压缩挂点: 分组配置 + 全局 master 均开启时生效; fail-open, 不影响转发
	compress.MaybeApply(request, group)
	request, replayState := prepareHTTPReplay(inboundType, c.GetInt("api_key_id"), group.ID, request.Model, request)
	iterator := newRelayIterator(group, c.GetInt("api_key_id"), request.Model, replayState)
	if iterator.Len() == 0 {
		resp.ErrorWithCode(c, http.StatusServiceUnavailable, CodeRelayNoAvailableChannel, "no available channel")
		return nil, false
	}
	return buildRelayHandler(inboundType, c, group, op.ProtocolRoutingEnabled(),
		rawBody, request, inAdapter, replayState, iterator), true
}

func newRelayIterator(group dbmodel.Group, apiKeyID int, requestModel string, replayState *wsConversationState) *balancer.Iterator {
	var sticky *balancer.SessionEntry
	if replayState != nil {
		sticky = responsesReplayStateToSticky(replayState)
		if sticky != nil {
			log.Debugf("HTTP replay sticky routing preference (channel=%d, key=%d)", sticky.ChannelID, sticky.ChannelKeyID)
		}
	}
	return balancer.NewIteratorWithPreference(group, apiKeyID, requestModel, sticky)
}

func buildRelayHandler(
	inboundType inbound.InboundType,
	c *gin.Context,
	group dbmodel.Group,
	protocolRoutingEnabled bool,
	rawBody []byte,
	request *model.InternalLLMRequest,
	inAdapter model.Inbound,
	replayState *wsConversationState,
	iterator *balancer.Iterator,
) *relayHandler {
	apiKeyID := c.GetInt("api_key_id")
	heartbeat := startEarlyHeartbeat(c, request.Stream != nil && *request.Stream)
	metrics := NewRelayMetrics(apiKeyID, request.Model, rawBody, request)
	if replayState != nil {
		metrics.SetWSMode(dbmodel.RelayLogWSModeReplay)
		metrics.SetWSRecovery(dbmodel.RelayLogWSRecoveryReplay)
	}
	requestContext := &relayRequest{
		c: c, inAdapter: inAdapter, internalRequest: request, metrics: metrics,
		apiKeyID: apiKeyID, requestModel: request.Model, groupID: group.ID,
		groupSessionTTL: group.SessionKeepTime, iter: iterator, rawBody: rawBody, heartbeat: heartbeat,
	}
	return &relayHandler{
		inboundType: inboundType, c: c, group: group,
		protocolRoutingEnabled: protocolRoutingEnabled, replayState: replayState,
		iterator: iterator, heartbeat: heartbeat, metrics: metrics, request: requestContext,
		passthroughRequired: request.HasOpenAIResponsesPassthrough(),
		maxRetries:          sameChannelRetryLimit(group.RetryEnabled, group.MaxRetries),
	}
}

func (h *relayHandler) run() {
	for h.iterator.Next() {
		if h.c.Request.Context().Err() != nil {
			log.Debugf("request context canceled, stopping retry")
			h.metrics.SaveWithChannelStats(h.c.Request.Context(), false, context.Canceled, h.iterator.Attempts(), false)
			return
		}
		if h.processCandidate() {
			return
		}
	}
	writeExhaustedRelayError(exhaustedRelayInput{
		c: h.c, heartbeat: h.heartbeat, metrics: h.metrics, attempts: h.iterator.Attempts(),
		lastErr: h.lastErr, lastResult: h.lastResult, capacitySkipped: h.capacitySkipped,
		rateSkipped: h.rateSkipped, passthroughRequired: h.passthroughRequired,
		passthroughCapableFound: h.passthroughCapable,
	})
}

func (h *relayHandler) processCandidate() bool {
	item := h.iterator.Item()
	channel, err := op.ChannelGet(item.ChannelID, h.c.Request.Context())
	if err != nil {
		log.Warnf("failed to get channel %d: %v", item.ChannelID, err)
		h.iterator.Skip(item.ChannelID, 0, fmt.Sprintf("channel_%d", item.ChannelID), fmt.Sprintf("channel not found: %v", err))
		h.lastErr = err
		return false
	}
	if !channel.Enabled {
		h.iterator.Skip(channel.ID, 0, channel.Name, "channel disabled")
		return false
	}
	legacyEligible, reason := legacyChannelEligibility(channel, h.request.internalRequest, h.passthroughRequired)
	key, plans := h.selectCandidateAttempt(channel, item.ModelName, legacyEligible)
	if key.ChannelKey == "" || len(plans) == 0 {
		if key.ChannelKey != "" {
			h.iterator.Skip(channel.ID, key.ID, channel.Name, reason)
		}
		return false
	}
	for _, plan := range plans {
		if plan.UpstreamProtocol() == protocol.OpenAIResponse {
			h.passthroughCapable = true
		}
	}
	if !h.acquireCandidate(channel, key) {
		return false
	}
	result := runSameChannelAttempts(h.c.Request.Context(), h.request, channel, key, plans,
		h.group.FirstTokenTimeOut, h.maxRetries)
	usedPlan := plans[0]
	if result.Plan != nil {
		usedPlan = result.Plan
	}
	return h.handleAttemptResult(channel, key, usedPlan, result)
}

func (h *relayHandler) selectCandidateAttempt(channel *dbmodel.Channel, upstreamModel string, legacyEligible bool) (dbmodel.ChannelKey, []*protocolroute.AttemptPlan) {
	log.Debugf("request model %s, mode: %d, forwarding to channel: %s model: %s (attempt %d/%d, sticky=%t)",
		h.request.requestModel, h.group.Mode, channel.Name, upstreamModel,
		h.iterator.Index()+1, h.iterator.Len(), h.iterator.IsSticky())
	return selectChannelAttempt(channelAttemptInput{
		channel: channel, upstreamModel: upstreamModel, requestModel: h.request.requestModel,
		request: h.request.internalRequest, iterator: h.iterator, group: h.group,
		protocolRoutingEnabled: h.protocolRoutingEnabled, legacyEligible: legacyEligible,
		responsesPassthroughRequired: h.passthroughRequired,
	})
}

func (h *relayHandler) acquireCandidate(channel *dbmodel.Channel, key dbmodel.ChannelKey) bool {
	if !balancer.TryAcquireChannel(channel.ID, channel.MaxConcurrency) {
		h.capacitySkipped = true
		h.iterator.SkipCapacity(channel.ID, key.ID, channel.Name,
			fmt.Sprintf("channel at max concurrency (%d)", channel.MaxConcurrency))
		return false
	}
	if balancer.TryConsumeChannelRPM(channel.ID, channel.MaxRPM, time.Now()) {
		return true
	}
	balancer.ReleaseChannel(channel.ID)
	h.rateSkipped = true
	h.iterator.SkipRateLimit(channel.ID, key.ID, channel.Name,
		fmt.Sprintf("channel at max rpm (%d)", channel.MaxRPM))
	return false
}

func (h *relayHandler) handleAttemptResult(channel *dbmodel.Channel, key dbmodel.ChannelKey, plan *protocolroute.AttemptPlan, result attemptResult) bool {
	if !result.Success && !result.Written && !result.Canceled && !result.ResetConversation {
		failureKind := circuitFailureKind(h.group.RetryEnabled, result.StatusCode)
		balancer.RecordFailure(channel.ID, key.ID, plan.UpstreamModel(), failureKind)
		outlierwindow.Report(channel.ID, false, result.StatusCode, time.Now())
		if failureKind == balancer.FailureHard {
			maybeLearnManagedRoute(h.c.Request.Context(), channel.ID, plan.UpstreamModel(), h.inboundType, result.Err)
		}
	}
	switch classifyAttemptResult(result) {
	case attemptActionSuccess:
		return h.handleSuccessfulAttempt(channel, key, result)
	case attemptActionCanceled:
		h.metrics.SaveWithChannelStats(h.c.Request.Context(), false, result.Err, h.iterator.Attempts(), false)
		return true
	case attemptActionResetConversation:
		h.metrics.SaveWithChannelStats(h.c.Request.Context(), false, result.Err, h.iterator.Attempts(), false)
		if publicErr, ok := classifyWSPublicError(result.Err, result.StatusCode); ok {
			h.heartbeat.FlushOrError(h.c, publicErr.Status, publicErr.Message)
		} else {
			h.heartbeat.FlushOrError(h.c, result.StatusCode, result.Err.Error())
		}
		return true
	case attemptActionWritten:
		h.metrics.SaveWithChannelStats(h.c.Request.Context(), false, result.Err, h.iterator.Attempts(), false)
		return true
	}
	h.lastErr = result.Err
	h.lastResult = result
	return false
}

type attemptAction uint8

const (
	attemptActionContinue attemptAction = iota
	attemptActionSuccess
	attemptActionCanceled
	attemptActionResetConversation
	attemptActionWritten
)

func classifyAttemptResult(result attemptResult) attemptAction {
	if result.Success {
		return attemptActionSuccess
	}
	if result.Canceled {
		return attemptActionCanceled
	}
	if result.ResetConversation {
		return attemptActionResetConversation
	}
	if result.Written {
		return attemptActionWritten
	}
	return attemptActionContinue
}

func (h *relayHandler) handleSuccessfulAttempt(channel *dbmodel.Channel, key dbmodel.ChannelKey, result attemptResult) bool {
	outlierwindow.Report(channel.ID, true, result.StatusCode, time.Now())
	saveHTTPReplayState(httpReplaySaveInput{
		ctx: h.c.Request.Context(), inboundType: h.inboundType, request: h.request.internalRequest,
		inAdapter: h.request.inAdapter, metrics: h.metrics, previousState: h.replayState,
		apiKeyID: h.request.apiKeyID, groupID: h.group.ID, groupTTL: h.group.SessionKeepTime,
		requestModel: h.request.requestModel, channelID: channel.ID, channelKeyID: key.ID,
	})
	h.metrics.SaveWithChannelStats(h.c.Request.Context(), true, nil, h.iterator.Attempts(), false)
	return true
}
