package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tmaxmax/go-sse"

	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// handleResponse 处理非流式响应
func (ra *relayAttempt) handleResponse(ctx context.Context, response *http.Response) error {
	internalResponse, err := ra.outAdapter.TransformResponse(ctx, response)
	if err != nil {
		log.Warnf("failed to transform response: %v", err)
		return fmt.Errorf("failed to transform outbound response: %w", err)
	}

	inResponse, err := ra.inAdapter.TransformResponse(ctx, internalResponse)
	if err != nil {
		log.Warnf("failed to transform response: %v", err)
		return fmt.Errorf("failed to transform inbound response: %w", err)
	}

	ra.c.Data(http.StatusOK, "application/json", inResponse)
	return nil
}

// collectResponse 收集响应信息
func (ra *relayAttempt) collectResponse() {
	if ra == nil || ra.inAdapter == nil || ra.metrics == nil {
		return
	}
	if !ra.responseCollected.CompareAndSwap(false, true) {
		return
	}
	internalResponse, err := ra.inAdapter.GetInternalResponse(ra.requestContext())
	if err != nil {
		log.Debugf("collectResponse: failed to get internal response: %v", err)
		return
	}
	if internalResponse == nil {
		log.Debugf("collectResponse: internal response is nil (stream may not be complete)")
		return
	}

	actualModel := strings.TrimSpace(internalResponse.Model)
	if actualModel == "" && ra.internalRequest != nil {
		actualModel = strings.TrimSpace(ra.internalRequest.Model)
	}
	ra.metrics.SetInternalResponse(internalResponse, actualModel)
}

func (ra *relayAttempt) collectOpenAIResponsesPassthroughMetrics(ctx context.Context, rawStream []byte) {
	if len(rawStream) == 0 {
		return
	}
	outEventAdapter, outOk := ra.outAdapter.(model.OutboundStreamEventTransformer)
	inEventAdapter, inOk := ra.inAdapter.(model.InboundStreamEventTransformer)
	if outOk && inOk {
		readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
		for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
			if err != nil {
				log.Debugf("openai responses passthrough metrics parse skipped: %v", err)
				return
			}
			if events, terr := outEventAdapter.TransformStreamEvent(ctx, []byte(ev.Data)); terr == nil && len(events) > 0 {
				_, _ = inEventAdapter.TransformStreamEvents(ctx, events)
			}
		}
		return
	}
	readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
	for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
		if err != nil {
			log.Debugf("openai responses passthrough metrics parse skipped: %v", err)
			return
		}
		if internalStream, terr := ra.outAdapter.TransformStream(ctx, []byte(ev.Data)); terr == nil && internalStream != nil {
			_, _ = ra.inAdapter.TransformStream(ctx, internalStream)
		}
	}
}

var (
	responsesPassthroughTerminalEvents = map[string]struct{}{
		"response.completed":  {},
		"response.failed":     {},
		"response.incomplete": {},
		"error":               {},
	}
	anthropicPassthroughTerminalEvents = map[string]struct{}{
		"message_stop": {},
		"error":        {},
	}
)

// streamReachedTerminalEvent 报告缓存的原始 SSE 流是否已包含协议终态事件。
// 客户端 SDK 收到终态事件后会立即断连而不等上游 EOF，断连取消会沿出站请求
// 传播打断上游读取；此时读取被取消不代表流未完成。
func streamReachedTerminalEvent(rawStream []byte, terminalTypes map[string]struct{}) bool {
	if len(rawStream) == 0 {
		return false
	}
	readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
	for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
		if err != nil {
			break
		}
		typ := strings.TrimSpace(ev.Type)
		if typ == "" {
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(ev.Data), &head) == nil {
				typ = head.Type
			}
		}
		if _, ok := terminalTypes[typ]; ok {
			return true
		}
	}
	return false
}

func (ra *relayAttempt) collectAnthropicPassthroughMetrics(ctx context.Context, rawStream []byte) {
	if len(rawStream) == 0 {
		return
	}
	outEventAdapter, outOk := ra.outAdapter.(model.OutboundStreamEventTransformer)
	inEventAdapter, inOk := ra.inAdapter.(model.InboundStreamEventTransformer)
	if outOk && inOk {
		readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
		for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
			if err != nil {
				log.Debugf("anthropic passthrough metrics parse skipped: %v", err)
				return
			}
			if events, terr := outEventAdapter.TransformStreamEvent(ctx, []byte(ev.Data)); terr == nil && len(events) > 0 {
				_, _ = inEventAdapter.TransformStreamEvents(ctx, events)
			}
		}
		return
	}
	readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
	for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
		if err != nil {
			log.Debugf("anthropic passthrough metrics parse skipped: %v", err)
			return
		}
		if internalStream, terr := ra.outAdapter.TransformStream(ctx, []byte(ev.Data)); terr == nil && internalStream != nil {
			_, _ = ra.inAdapter.TransformStream(ctx, internalStream)
		}
	}
}
