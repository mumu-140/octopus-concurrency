package relay

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/outlierwindow"
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

func Handler(inboundType inbound.InboundType, c *gin.Context) {
	// 解析请求
	rawBody, internalRequest, inAdapter, err := parseRequest(inboundType, c)
	if err != nil {
		return
	}
	supportedModels := c.GetString("supported_models")
	if supportedModels != "" {
		supportedModelsArray := strings.Split(supportedModels, ",")
		if !slices.Contains(supportedModelsArray, internalRequest.Model) {
			resp.ErrorWithCode(c, http.StatusBadRequest, CodeRelayModelNotSupported, "model not supported")
			return
		}
	}

	requestModel := internalRequest.Model
	apiKeyID := c.GetInt("api_key_id")

	// 获取通道分组
	group, err := op.GroupGetEnabledMap(requestModel, c.Request.Context())
	if err != nil {
		resp.ErrorWithCode(c, http.StatusNotFound, CodeRelayModelNotFound, "model not found")
		return
	}
	policyState, _ := op.ProtocolPolicyRuntimeSnapshot()
	policySnapshot, routingMode := buildProtocolPolicySnapshot(policyState, group.ID)

	// === HTTP Replay 机制 ===
	// 当 HTTP 请求携带 previous_response_id 时，尝试从本地加载上一次成功的 replay 状态，
	// 优先路由到同一渠道/key，并将请求转为自包含形式（合并历史，移除 previous_response_id）。
	var responsesReplayState *wsConversationState
	if inboundType == inbound.InboundTypeOpenAIResponse && internalRequest.RawAPIFormat == model.APIFormatOpenAIResponse {
		if prevID := internalRequest.OpenAIPreviousResponseID(); prevID != "" {
			responsesReplayState = resolveResponsesReplayState(apiKeyID, group.ID, requestModel, internalRequest)
			if responsesReplayState != nil {
				log.Debugf("loaded HTTP replay state (apikey=%d, group=%d, model=%s, previous_response_id=%s, channel=%d, key=%d)",
					apiKeyID, group.ID, requestModel, prevID, responsesReplayState.ChannelID, responsesReplayState.ChannelKeyID)
				// 转换请求为自包含形式（移除 previous_response_id，合并历史）
				// BuildReplayRequest 返回 nil 表示合并失败，应保留原始请求
				if replayed := responsesReplayState.BuildReplayRequest(internalRequest); replayed != nil {
					internalRequest = replayed
					log.Debugf("HTTP replay request transformed (apikey=%d, removed previous_response_id, merged history)", apiKeyID)
				} else {
					log.Warnf("HTTP replay history merge failed (apikey=%d, group=%d, model=%s, previous_response_id=%s), keeping original request",
						apiKeyID, group.ID, requestModel, prevID)
					responsesReplayState = nil // 放弃 replay，使用原始请求
				}
			} else {
				log.Debugf("no HTTP replay state found (apikey=%d, group=%d, model=%s, previous_response_id=%s)",
					apiKeyID, group.ID, requestModel, prevID)
			}
		}
	}

	// 创建迭代器（策略排序 + 粘性优先）
	// 如果有 replay state，注入为 sticky 偏好
	var preferredSticky *balancer.SessionEntry
	if responsesReplayState != nil {
		preferredSticky = responsesReplayStateToSticky(responsesReplayState)
		if preferredSticky != nil {
			log.Debugf("HTTP replay sticky routing preference (channel=%d, key=%d)", preferredSticky.ChannelID, preferredSticky.ChannelKeyID)
		}
	}
	iter := balancer.NewIteratorWithPreference(group, apiKeyID, requestModel, preferredSticky)
	if iter.Len() == 0 {
		resp.ErrorWithCode(c, http.StatusServiceUnavailable, CodeRelayNoAvailableChannel, "no available channel")
		return
	}

	// === 早期心跳 ===
	// 在所有 forward / 重试 / 退避之前启动早期心跳协程，覆盖前置阶段（连接慢、failover、退避叠加）
	// 期间向客户端发 SSE 注释字节，避免被 Cloudflare 在 120s 零字节阈值上判 524。
	// 仅对流式请求生效；非流式无法发送 SSE 注释（破坏 application/json 协议），
	// 不施加任何本地超时——上游慢响应应让其自然完成或由上游/CF 自身处理。
	isStream := internalRequest.Stream != nil && *internalRequest.Stream
	hb := startEarlyHeartbeat(c, isStream)
	defer hb.Stop()

	// 初始化 Metrics
	metrics := NewRelayMetrics(apiKeyID, requestModel, rawBody, internalRequest)
	// 如果触发了 HTTP replay，记录 ws_mode=replay 和 ws_recovery=replay
	if responsesReplayState != nil {
		metrics.SetWSMode(dbmodel.RelayLogWSModeReplay)
		metrics.SetWSRecovery(dbmodel.RelayLogWSRecoveryReplay)
	}
	responsesPassthroughRequired := internalRequest.HasOpenAIResponsesPassthrough()
	responsesPassthroughCapableFound := false

	// 请求级上下文
	req := &relayRequest{
		c:               c,
		inAdapter:       inAdapter,
		internalRequest: internalRequest,
		metrics:         metrics,
		apiKeyID:        apiKeyID,
		requestModel:    requestModel,
		groupID:         group.ID,
		groupSessionTTL: group.SessionKeepTime,
		iter:            iter,
		rawBody:         rawBody,
		heartbeat:       hb,
	}

	var lastErr error
	var lastResult attemptResult
	capacitySkipped := false
	rateSkipped := false

	// 同通道重试次数：启用时使用配置值，否则 1 次（不重试）
	maxSameChannelRetries := 1
	if group.RetryEnabled {
		maxSameChannelRetries = group.MaxRetries
		if maxSameChannelRetries <= 0 {
			maxSameChannelRetries = 3
		}
	}

	for iter.Next() {
		select {
		case <-c.Request.Context().Done():
			log.Debugf("request context canceled, stopping retry")
			metrics.SaveWithChannelStats(c.Request.Context(), false, context.Canceled, iter.Attempts(), false)
			return
		default:
		}

		item := iter.Item()

		// 获取通道
		channel, err := op.ChannelGet(item.ChannelID, c.Request.Context())
		if err != nil {
			log.Warnf("failed to get channel %d: %v", item.ChannelID, err)
			iter.Skip(item.ChannelID, 0, fmt.Sprintf("channel_%d", item.ChannelID), fmt.Sprintf("channel not found: %v", err))
			lastErr = err
			continue
		}
		if !channel.Enabled {
			iter.Skip(channel.ID, 0, channel.Name, "channel disabled")
			continue
		}
		legacyEligible := outbound.Get(channel.Type) != nil
		legacyReason := ""
		if !legacyEligible {
			legacyReason = fmt.Sprintf("unsupported channel type: %d", channel.Type)
		}
		if responsesPassthroughRequired && channel.Type != outbound.OutboundTypeOpenAIResponse {
			legacyEligible = false
			legacyReason = "openai responses passthrough required"
		}
		if internalRequest.IsEmbeddingRequest() && !outbound.IsEmbeddingChannelType(channel.Type) {
			legacyEligible = false
			legacyReason = "channel type not compatible with embedding request"
		}
		if internalRequest.IsChatRequest() && !outbound.IsChatChannelType(channel.Type) {
			legacyEligible = false
			legacyReason = "channel type not compatible with chat request"
		}
		if routingMode != protocolroute.RoutingAdaptive && !legacyEligible {
			iter.Skip(channel.ID, 0, channel.Name, legacyReason)
			continue
		}

		log.Debugf("request model %s, mode: %d, forwarding to channel: %s model: %s (attempt %d/%d, sticky=%t)",
			requestModel, group.Mode, channel.Name, item.ModelName,
			iter.Index()+1, iter.Len(), iter.IsSticky())

		selectOpts := dbmodel.ChannelKeySelectOptions{
			ExcludeKeyIDs:  make(map[int]struct{}),
			PreferredKeyID: iter.StickyKeyID(),
		}
		var usedKey dbmodel.ChannelKey
		var attemptPlan *protocolroute.AttemptPlan
		channelPolicy := protocolPolicyForChannel(policyState, channel.ID)
		legacyConfig, adaptiveProfiles := buildAttemptConfigs(channel, channelPolicy)
		for {
			usedKey = channel.GetChannelKey(selectOpts)
			if usedKey.ChannelKey == "" {
				break
			}
			if !iter.SkipCircuitBreak(channel.ID, usedKey.ID, channel.Name) {
				decision := resolveRelayAttemptPlan(relayPlanInput{
					Snapshot:       policySnapshot,
					Mode:           routingMode,
					ChannelID:      channel.ID,
					ChannelKeyID:   usedKey.ID,
					ChannelType:    channel.Type,
					RequestedModel: requestModel,
					UpstreamModel:  item.ModelName,
					Request:        internalRequest,
					LegacyConfig:   legacyConfig,
					Profiles:       adaptiveProfiles,
					LegacyEligible: legacyEligible,
				})
				if !decision.Incompatible && decision.Plan != nil {
					attemptPlan = decision.Plan
					if !responsesPassthroughRequired || attemptPlan.UpstreamProtocol() == protocol.OpenAIResponse {
						break
					}
				}
				selectOpts.ExcludeKeyIDs[usedKey.ID] = struct{}{}
				usedKey = dbmodel.ChannelKey{}
				continue
			}
			selectOpts.ExcludeKeyIDs[usedKey.ID] = struct{}{}
			usedKey = dbmodel.ChannelKey{}
		}
		if usedKey.ChannelKey == "" {
			if len(selectOpts.ExcludeKeyIDs) == 0 {
				iter.Skip(channel.ID, 0, channel.Name, "no available key")
			}
			continue
		}
		if attemptPlan.UpstreamProtocol() == protocol.OpenAIResponse {
			responsesPassthroughCapableFound = true
		}

		if !balancer.TryAcquireChannel(channel.ID, channel.MaxConcurrency) {
			capacitySkipped = true
			iter.SkipCapacity(channel.ID, usedKey.ID, channel.Name,
				fmt.Sprintf("channel at max concurrency (%d)", channel.MaxConcurrency))
			continue
		}
		if !balancer.TryConsumeChannelRPM(channel.ID, channel.MaxRPM, time.Now()) {
			balancer.ReleaseChannel(channel.ID)
			rateSkipped = true
			iter.SkipRateLimit(channel.ID, usedKey.ID, channel.Name,
				fmt.Sprintf("channel at max rpm (%d)", channel.MaxRPM))
			continue
		}

		// 一个 slot 覆盖该渠道的全部内部重试，并在切换候选前释放。
		result := func() attemptResult {
			defer balancer.ReleaseChannel(channel.ID)
			var result attemptResult
			for retryNum := 0; retryNum < maxSameChannelRetries; retryNum++ {
				// 重试前等待退避
				if retryNum > 0 {
					delay := computeBackoff(retryNum, result.RetryAfter)
					log.Infof("same-channel retry %d/%d for %s, waiting %v",
						retryNum, maxSameChannelRetries, channel.Name, delay)
					select {
					case <-c.Request.Context().Done():
						log.Debugf("request context canceled during retry backoff")
						return attemptResult{Canceled: true, Err: context.Canceled}
					case <-time.After(delay):
					}
				}

				ra, attemptErr := newRelayAttempt(req, channel, usedKey, attemptPlan, group.FirstTokenTimeOut)
				if attemptErr != nil {
					return attemptResult{Err: attemptErr, StatusCode: http.StatusInternalServerError}
				}

				result = ra.attempt()
				if result.Success || result.Written || result.Canceled || result.ResetConversation || result.FirstTokenTimeout || !isRetryableStatus(result.StatusCode) {
					break
				}
			}
			return result
		}()

		// 同通道重试耗尽后记录熔断器失败
		if !result.Success && !result.Written && !result.Canceled && !result.ResetConversation {
			failureKind := circuitFailureKind(group.RetryEnabled, result.StatusCode)
			balancer.RecordFailure(channel.ID, usedKey.ID, attemptPlan.UpstreamModel(), failureKind)
			outlierwindow.Report(channel.ID, false, result.StatusCode, time.Now())
			if failureKind == balancer.FailureHard {
				maybeLearnManagedRoute(c.Request.Context(), channel.ID, attemptPlan.UpstreamModel(), inboundType, result.Err)
			}
		}

		if result.Success {
			outlierwindow.Report(channel.ID, true, result.StatusCode, time.Now())

			// === HTTP Replay 状态保存 ===
			// 成功后，如果是 OpenAI Responses HTTP 请求，保存 replay 状态供后续续接
			// 注意：exact replay 请求成功后也需要保存新状态，否则只能续接一轮
			// 优先使用 metrics.InternalResponse（streaming 安全），避免二次 GetInternalResponse 消耗聚合器
			if inboundType == inbound.InboundTypeOpenAIResponse &&
				req.internalRequest.RawAPIFormat == model.APIFormatOpenAIResponse {
				internalResponse := metrics.InternalResponse
				if internalResponse == nil {
					var err error
					internalResponse, err = inAdapter.GetInternalResponse(c.Request.Context())
					if err != nil {
						log.Debugf("failed to get internal response for replay state save: %v", err)
					}
				}
				if internalResponse != nil {
					// 如果是 exact replay 请求，基于已有状态继续累积
					var newState *wsConversationState
					if req.internalRequest.IsOpenAIExactReplayRequest() && responsesReplayState != nil {
						newState = cloneWSConversationState(responsesReplayState)
						if newState != nil {
							newState.ChannelID = channel.ID
							newState.ChannelKeyID = usedKey.ID
						}
					}
					if newState == nil {
						newState = &wsConversationState{
							RequestModel: requestModel,
							ChannelID:    channel.ID,
							ChannelKeyID: usedKey.ID,
						}
					}
					newState.ApplySuccessfulTurn(req.internalRequest, internalResponse)
					if newState.LastResponseID != "" {
						ttl := wsConversationStateTTL(group.SessionKeepTime)
						storeResponsesReplayState(apiKeyID, group.ID, requestModel, newState, ttl)
						log.Debugf("saved HTTP replay state (apikey=%d, group=%d, model=%s, response_id=%s, channel=%d, key=%d, ttl=%v, is_replay=%t)",
							apiKeyID, group.ID, requestModel, newState.LastResponseID, channel.ID, usedKey.ID, ttl, req.internalRequest.IsOpenAIExactReplayRequest())
					}
				}
			}

			metrics.SaveWithChannelStats(c.Request.Context(), true, nil, iter.Attempts(), false)
			return
		}
		if result.Canceled {
			metrics.SaveWithChannelStats(c.Request.Context(), false, result.Err, iter.Attempts(), false)
			return
		}
		if result.ResetConversation {
			metrics.SaveWithChannelStats(c.Request.Context(), false, result.Err, iter.Attempts(), false)
			if publicErr, ok := classifyWSPublicError(result.Err, result.StatusCode); ok {
				hb.FlushOrError(c, publicErr.Status, publicErr.Message)
			} else {
				hb.FlushOrError(c, result.StatusCode, result.Err.Error())
			}
			return
		}
		if result.Written {
			metrics.SaveWithChannelStats(c.Request.Context(), false, result.Err, iter.Attempts(), false)
			return
		}
		lastErr = result.Err
		lastResult = result
	}

	// 所有候选通道均失败
	if responsesPassthroughRequired && !responsesPassthroughCapableFound {
		err := fmt.Errorf("openai responses native tools require an openai responses channel")
		metrics.SaveWithChannelStats(c.Request.Context(), false, err, iter.Attempts(), false)
		hb.FlushOrError(c, http.StatusBadRequest, "当前请求包含 OpenAI Responses 原生工具，仅支持 OpenAI Responses 通道直通")
		return
	}
	metrics.SaveWithChannelStats(c.Request.Context(), false, lastErr, iter.Attempts(), false)
	if lastErr == nil && rateSkipped {
		c.Header("Retry-After", "60")
		hb.FlushOrError(c, http.StatusTooManyRequests, "all eligible channels are at max rpm")
		return
	}
	if lastErr == nil && capacitySkipped {
		c.Header("Retry-After", "1")
		hb.FlushOrError(c, http.StatusServiceUnavailable, "all eligible channels are at max concurrency")
		return
	}

	// 透传 429/503 状态码和 Retry-After 头，让客户端 SDK 的重试机制接管
	if isPassthroughStatus(lastResult.StatusCode) {
		if lastResult.RetryAfter > 0 {
			c.Header("Retry-After", fmt.Sprintf("%d", int(lastResult.RetryAfter.Seconds())))
		}
		hb.FlushOrError(c, lastResult.StatusCode, "channel failed")
		return
	}
	if lastResult.StatusCode > 0 {
		hb.FlushOrError(c, lastResult.StatusCode, "channel failed")
		return
	}
	hb.FlushOrError(c, http.StatusBadGateway, "channel failed")
}
