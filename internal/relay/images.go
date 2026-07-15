package relay

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/relay/bodycache"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/gin-gonic/gin"
)

// ImagesHandler 是 OpenAI Images API 的统一 relay 入口。
// endpoint 形如：/images/generations、/images/edits、/images/variations（不含 /v1 前缀）。
func ImagesHandler(endpoint string, c *gin.Context) {
	ctx := c.Request.Context()

	apiKeyID := c.GetInt("api_key_id")

	// 缓存请求体，支持多次重试重放
	bc, err := bodycache.New(c.Request.Body)
	if err != nil {
		var tooLarge *bodycache.BodyTooLargeError
		if errors.As(err, &tooLarge) {
			resp.Error(c, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() {
		if cerr := bc.Close(); cerr != nil {
			log.Warnf("failed to close images body cache: %v", cerr)
		}
	}()

	contentType := c.GetHeader("Content-Type")
	isMultipart := strings.Contains(strings.ToLower(contentType), "multipart/form-data")

	// 解析 requestModel 与 stream（严格模式：model 必填）
	var (
		requestModel string
		stream       bool
		boundary     string
		jsonPayload  map[string]any
	)
	if isMultipart {
		_, params, perr := mime.ParseMediaType(contentType)
		if perr != nil {
			resp.Error(c, http.StatusBadRequest, "invalid multipart content-type")
			return
		}
		boundary = strings.TrimSpace(params["boundary"])
		if boundary == "" {
			resp.Error(c, http.StatusBadRequest, "invalid multipart boundary")
			return
		}
		m, s, perr := parseMultipartModelAndStream(bc, boundary)
		if perr != nil {
			resp.Error(c, http.StatusBadRequest, perr.Error())
			return
		}
		requestModel = m
		stream = s
	} else {
		payload, m, s, perr := parseJSONModelAndStream(bc)
		if perr != nil {
			resp.Error(c, http.StatusBadRequest, perr.Error())
			return
		}
		jsonPayload = payload
		requestModel = m
		stream = s
	}

	// supported_models 校验（复用 APIKeyAuth 注入）
	supportedModels := strings.TrimSpace(c.GetString("supported_models"))
	if supportedModels != "" {
		supportedModelsArray := strings.Split(supportedModels, ",")
		if !slices.Contains(supportedModelsArray, requestModel) {
			resp.ErrorWithCode(c, http.StatusBadRequest, CodeRelayModelNotSupported, "model not supported")
			return
		}
	}

	// 获取通道分组
	group, err := op.GroupGetEnabledMap(requestModel, ctx)
	if err != nil {
		resp.ErrorWithCode(c, http.StatusNotFound, CodeRelayModelNotFound, "model not found")
		return
	}

	// 创建迭代器（策略排序 + 粘性优先）
	iter := balancer.NewIterator(group, apiKeyID, requestModel)
	if iter.Len() == 0 {
		resp.ErrorWithCode(c, http.StatusServiceUnavailable, CodeRelayNoAvailableChannel, "no available channel")
		return
	}

	// 初始化 Metrics（Images 独立，避免 b64_json 内存膨胀）
	metrics := newImagesRelayMetrics(apiKeyID, requestModel)
	metrics.RequestContent = buildImagesRequestContentForLog(isMultipart, bc, jsonPayload)

	// === 早期心跳 ===
	// 流式：启动早期心跳协程，覆盖前置阶段（连接慢、failover、退避）期间向客户端发 SSE 注释字节
	// 非流式：无法发送 SSE 注释（破坏 application/json 协议），不施加本地超时
	hb := startEarlyHeartbeat(c, stream)
	defer hb.Stop()

	var lastErr error

	for iter.Next() {
		select {
		case <-ctx.Done():
			log.Debugf("request context canceled, stopping retry")
			metrics.SaveWithChannelStats(ctx, false, context.Canceled, iter.Attempts(), false)
			return
		default:
		}

		item := iter.Item()

		// 获取通道
		channel, err := op.ChannelGet(item.ChannelID, ctx)
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

		// channel.Type 限制：仅 OpenAI Chat/Responses
		if channel.Type != outbound.OutboundTypeOpenAIChat && channel.Type != outbound.OutboundTypeOpenAIResponse {
			iter.Skip(channel.ID, 0, channel.Name, fmt.Sprintf("unsupported channel type: %d", channel.Type))
			continue
		}

		usedKey := channel.GetChannelKey()
		if usedKey.ChannelKey == "" {
			iter.Skip(channel.ID, 0, channel.Name, "no available key")
			continue
		}

		// 熔断检查（熔断 key 使用 actualModel=item.ModelName）
		if iter.SkipCircuitBreak(channel.ID, usedKey.ID, channel.Name) {
			continue
		}

		log.Debugf("images request model %s, mode: %d, forwarding to channel: %s model: %s (attempt %d/%d, sticky=%t, stream=%t)",
			requestModel, group.Mode, channel.Name, item.ModelName,
			iter.Index()+1, iter.Len(), iter.IsSticky(), stream)

		span := iter.StartAttempt(channel.ID, usedKey.ID, channel.Name)

		// 尝试一次转发
		statusCode, written, usage, upstreamCT, fwdErr := imagesAttempt(ctx, endpoint, c, bc, isMultipart, boundary, jsonPayload, stream, channel, usedKey.ChannelKey, group.FirstTokenTimeOut, metrics, item.ModelName, hb)

		// 更新 channel key 状态
		usedKey.StatusCode = statusCode
		usedKey.LastUseTimeStamp = time.Now().Unix()

		if fwdErr == nil {
			// ====== 成功 ======
			metrics.ActualModel = item.ModelName
			if usage != nil {
				metrics.SetUsageFromImages(item.ModelName, *usage)
			}
			metrics.ResponseContent = buildImagesResponseContentForLog(stream, upstreamCT, usage)

			usedKey.TotalCost += metrics.Stats.InputCost + metrics.Stats.OutputCost
			op.ChannelKeyUpdate(usedKey)

			span.End(model.AttemptSuccess, statusCode, "")

			// Channel 维度统计
			op.StatsChannelUpdate(channel.ID, model.StatsMetrics{
				WaitTime:       span.Duration().Milliseconds(),
				RequestSuccess: 1,
			})

			// 熔断器：记录成功
			balancer.RecordSuccess(channel.ID, usedKey.ID, item.ModelName)
			// 会话保持：更新粘性记录
			balancer.SetSticky(apiKeyID, requestModel, channel.ID, usedKey.ID)

			metrics.SaveWithChannelStats(ctx, true, nil, iter.Attempts(), false)
			return
		}

		// ====== 失败 ======
		op.ChannelKeyUpdate(usedKey)
		span.End(model.AttemptFailed, statusCode, fwdErr.Error())

		// Channel 维度统计
		op.StatsChannelUpdate(channel.ID, model.StatsMetrics{
			WaitTime:      span.Duration().Milliseconds(),
			RequestFailed: 1,
		})

		// 熔断器：记录失败
		balancer.RecordFailure(channel.ID, usedKey.ID, item.ModelName, circuitFailureKind(group.RetryEnabled, statusCode))

		if written {
			metrics.SaveWithChannelStats(ctx, false, fwdErr, iter.Attempts(), false)
			return
		}

		lastErr = fmt.Errorf("channel %s failed: %w", channel.Name, fwdErr)
	}

	// 所有通道都失败
	metrics.SaveWithChannelStats(ctx, false, lastErr, iter.Attempts(), false)
	writeFinalImagesError(c, hb, lastErr)
}
