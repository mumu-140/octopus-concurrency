package relay

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/transformer/inbound"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

func validateSupportedModel(c *gin.Context, requestedModel string) bool {
	supportedModels := c.GetString("supported_models")
	if supportedModels == "" || slices.Contains(strings.Split(supportedModels, ","), requestedModel) {
		return true
	}
	resp.ErrorWithCode(c, http.StatusBadRequest, CodeRelayModelNotSupported, "model not supported")
	return false
}

func prepareHTTPReplay(
	inboundType inbound.InboundType,
	apiKeyID int,
	groupID int,
	requestModel string,
	request *model.InternalLLMRequest,
) (*model.InternalLLMRequest, *wsConversationState) {
	if inboundType != inbound.InboundTypeOpenAIResponse || request.RawAPIFormat != model.APIFormatOpenAIResponse {
		return request, nil
	}
	previousID := request.OpenAIPreviousResponseID()
	if previousID == "" {
		return request, nil
	}

	state := resolveResponsesReplayState(apiKeyID, groupID, requestModel, request)
	if state == nil {
		log.Debugf("no HTTP replay state found (apikey=%d, group=%d, model=%s, previous_response_id=%s)",
			apiKeyID, groupID, requestModel, previousID)
		return request, nil
	}
	log.Debugf("loaded HTTP replay state (apikey=%d, group=%d, model=%s, previous_response_id=%s, channel=%d, key=%d)",
		apiKeyID, groupID, requestModel, previousID, state.ChannelID, state.ChannelKeyID)

	replayed := state.BuildReplayRequest(request)
	if replayed == nil {
		log.Warnf("HTTP replay history merge failed (apikey=%d, group=%d, model=%s, previous_response_id=%s), keeping original request",
			apiKeyID, groupID, requestModel, previousID)
		return request, nil
	}
	log.Debugf("HTTP replay request transformed (apikey=%d, removed previous_response_id, merged history)", apiKeyID)
	return replayed, state
}

func sameChannelRetryLimit(enabled bool, configured int) int {
	if !enabled {
		return 1
	}
	if configured <= 0 {
		return 3
	}
	return configured
}

func legacyChannelEligibility(channel *dbmodel.Channel, request *model.InternalLLMRequest, passthroughRequired bool) (bool, string) {
	if outbound.Get(channel.Type) == nil {
		return false, fmt.Sprintf("unsupported channel type: %d", channel.Type)
	}
	if passthroughRequired && channel.Type != outbound.OutboundTypeOpenAIResponse {
		return false, "openai responses passthrough required"
	}
	if request.IsEmbeddingRequest() && !outbound.IsEmbeddingChannelType(channel.Type) {
		return false, "channel type not compatible with embedding request"
	}
	if request.IsChatRequest() && !outbound.IsChatChannelType(channel.Type) {
		return false, "channel type not compatible with chat request"
	}
	return true, ""
}

type channelAttemptInput struct {
	channel                      *dbmodel.Channel
	upstreamModel                string
	requestModel                 string
	request                      *model.InternalLLMRequest
	iterator                     *balancer.Iterator
	policyState                  *dbmodel.ProtocolPolicyState
	policySnapshot               protocolroute.PolicySnapshot
	routingMode                  protocolroute.RoutingMode
	legacyEligible               bool
	responsesPassthroughRequired bool
}

func selectChannelAttempt(input channelAttemptInput) (dbmodel.ChannelKey, *protocolroute.AttemptPlan) {
	selectOpts := dbmodel.ChannelKeySelectOptions{
		ExcludeKeyIDs:  make(map[int]struct{}),
		PreferredKeyID: input.iterator.StickyKeyID(),
	}
	channelPolicy := protocolPolicyForChannel(input.policyState, input.channel.ID)
	legacyConfig, adaptiveProfiles := buildAttemptConfigs(input.channel, channelPolicy)
	for {
		usedKey := input.channel.GetChannelKey(selectOpts)
		if usedKey.ChannelKey == "" {
			if len(selectOpts.ExcludeKeyIDs) == 0 {
				input.iterator.Skip(input.channel.ID, 0, input.channel.Name, "no available key")
			}
			return dbmodel.ChannelKey{}, nil
		}
		if input.iterator.SkipCircuitBreak(input.channel.ID, usedKey.ID, input.channel.Name) {
			selectOpts.ExcludeKeyIDs[usedKey.ID] = struct{}{}
			continue
		}

		decision := resolveRelayAttemptPlan(relayPlanInput{
			Snapshot:       input.policySnapshot,
			Mode:           input.routingMode,
			ChannelID:      input.channel.ID,
			ChannelKeyID:   usedKey.ID,
			ChannelType:    input.channel.Type,
			RequestedModel: input.requestModel,
			UpstreamModel:  input.upstreamModel,
			Request:        input.request,
			LegacyConfig:   legacyConfig,
			Profiles:       adaptiveProfiles,
			LegacyEligible: input.legacyEligible,
		})
		if !decision.Incompatible && decision.Plan != nil &&
			(!input.responsesPassthroughRequired || decision.Plan.UpstreamProtocol() == protocol.OpenAIResponse) {
			return usedKey, decision.Plan
		}
		selectOpts.ExcludeKeyIDs[usedKey.ID] = struct{}{}
	}
}

func runSameChannelAttempts(
	ctx context.Context,
	request *relayRequest,
	channel *dbmodel.Channel,
	key dbmodel.ChannelKey,
	plan *protocolroute.AttemptPlan,
	firstTokenTimeout int,
	maxRetries int,
) attemptResult {
	defer balancer.ReleaseChannel(channel.ID)
	var result attemptResult
	for retryNum := 0; retryNum < maxRetries; retryNum++ {
		if retryNum > 0 {
			delay := computeBackoff(retryNum, result.RetryAfter)
			log.Infof("same-channel retry %d/%d for %s, waiting %v", retryNum, maxRetries, channel.Name, delay)
			select {
			case <-ctx.Done():
				log.Debugf("request context canceled during retry backoff")
				return attemptResult{Canceled: true, Err: context.Canceled}
			case <-time.After(delay):
			}
		}

		attempt, err := newRelayAttempt(request, channel, key, plan, firstTokenTimeout)
		if err != nil {
			return attemptResult{Err: err, StatusCode: http.StatusInternalServerError}
		}
		result = attempt.attempt()
		if result.Success || result.Written || result.Canceled || result.ResetConversation ||
			result.FirstTokenTimeout || !isRetryableStatus(result.StatusCode) {
			break
		}
	}
	return result
}

type httpReplaySaveInput struct {
	ctx           context.Context
	inboundType   inbound.InboundType
	request       *model.InternalLLMRequest
	inAdapter     model.Inbound
	metrics       *RelayMetrics
	previousState *wsConversationState
	apiKeyID      int
	groupID       int
	groupTTL      int
	requestModel  string
	channelID     int
	channelKeyID  int
}

func saveHTTPReplayState(input httpReplaySaveInput) {
	if input.inboundType != inbound.InboundTypeOpenAIResponse || input.request.RawAPIFormat != model.APIFormatOpenAIResponse {
		return
	}
	internalResponse := input.metrics.InternalResponse
	if internalResponse == nil {
		var err error
		internalResponse, err = input.inAdapter.GetInternalResponse(input.ctx)
		if err != nil {
			log.Debugf("failed to get internal response for replay state save: %v", err)
		}
	}
	if internalResponse == nil {
		return
	}

	var state *wsConversationState
	if input.request.IsOpenAIExactReplayRequest() && input.previousState != nil {
		state = cloneWSConversationState(input.previousState)
		if state != nil {
			state.ChannelID = input.channelID
			state.ChannelKeyID = input.channelKeyID
		}
	}
	if state == nil {
		state = &wsConversationState{
			RequestModel: input.requestModel,
			ChannelID:    input.channelID,
			ChannelKeyID: input.channelKeyID,
		}
	}
	state.ApplySuccessfulTurn(input.request, internalResponse)
	if state.LastResponseID == "" {
		return
	}

	ttl := wsConversationStateTTL(input.groupTTL)
	storeResponsesReplayState(input.apiKeyID, input.groupID, input.requestModel, state, ttl)
	log.Debugf("saved HTTP replay state (apikey=%d, group=%d, model=%s, response_id=%s, channel=%d, key=%d, ttl=%v, is_replay=%t)",
		input.apiKeyID, input.groupID, input.requestModel, state.LastResponseID,
		input.channelID, input.channelKeyID, ttl, input.request.IsOpenAIExactReplayRequest())
}

type exhaustedRelayInput struct {
	c                       *gin.Context
	heartbeat               *earlyHeartbeat
	metrics                 *RelayMetrics
	attempts                []dbmodel.ChannelAttempt
	lastErr                 error
	lastResult              attemptResult
	capacitySkipped         bool
	rateSkipped             bool
	passthroughRequired     bool
	passthroughCapableFound bool
}

func writeExhaustedRelayError(input exhaustedRelayInput) {
	if input.passthroughRequired && !input.passthroughCapableFound {
		err := fmt.Errorf("openai responses native tools require an openai responses channel")
		input.metrics.SaveWithChannelStats(input.c.Request.Context(), false, err, input.attempts, false)
		input.heartbeat.FlushOrError(input.c, http.StatusBadRequest, "当前请求包含 OpenAI Responses 原生工具，仅支持 OpenAI Responses 通道直通")
		return
	}
	input.metrics.SaveWithChannelStats(input.c.Request.Context(), false, input.lastErr, input.attempts, false)
	if input.lastErr == nil && input.rateSkipped {
		input.c.Header("Retry-After", "60")
		input.heartbeat.FlushOrError(input.c, http.StatusTooManyRequests, "all eligible channels are at max rpm")
		return
	}
	if input.lastErr == nil && input.capacitySkipped {
		input.c.Header("Retry-After", "1")
		input.heartbeat.FlushOrError(input.c, http.StatusServiceUnavailable, "all eligible channels are at max concurrency")
		return
	}
	if isPassthroughStatus(input.lastResult.StatusCode) {
		if input.lastResult.RetryAfter > 0 {
			input.c.Header("Retry-After", fmt.Sprintf("%d", int(input.lastResult.RetryAfter.Seconds())))
		}
		input.heartbeat.FlushOrError(input.c, input.lastResult.StatusCode, "channel failed")
		return
	}
	if input.lastResult.StatusCode > 0 {
		input.heartbeat.FlushOrError(input.c, input.lastResult.StatusCode, "channel failed")
		return
	}
	input.heartbeat.FlushOrError(input.c, http.StatusBadGateway, "channel failed")
}
