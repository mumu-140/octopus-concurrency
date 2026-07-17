package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/relay/stream"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/tmaxmax/go-sse"
)

// handleStreamResponseV2 uses StreamProcessor for unified stream handling.
func (ra *relayAttempt) handleStreamResponseV2(ctx context.Context, response *http.Response) error {
	defer ra.closeFirstTokenBudget()

	// Content-Type validation
	if ct := response.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "text/event-stream") {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		return fmt.Errorf("upstream returned non-SSE content-type %q for stream request: %s", ct, string(body))
	}

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
		Source:            stream.NewSSESource(response.Body, maxSSEEventSize),
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
		_ = response.Body.Close()
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

// handleStreamResponsePassthroughV2 uses StreamProcessor for unified passthrough handling.
// Works with any PassthroughCapable transformer (Anthropic, OpenAI Responses, etc.).
func (ra *relayAttempt) handleStreamResponsePassthroughV2(ctx context.Context, response *http.Response, cfg model.PassthroughConfig) error {
	defer ra.closeFirstTokenBudget()

	// Content-Type validation
	if ct := response.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "text/event-stream") {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
		return fmt.Errorf("upstream returned non-SSE content-type %q for stream request: %s", ct, string(body))
	}

	// Hand off early heartbeat
	ra.heartbeat.Hand()

	// Determine first token timeout
	var firstTokenTimeout time.Duration
	if ra.firstTokenTimeOutSec > 0 && ra.firstTokenBudget == nil {
		firstTokenTimeout = time.Duration(ra.firstTokenTimeOutSec) * time.Second
	}

	// Buffer for raw stream (for metrics collection)
	var rawStreamBuf bytes.Buffer

	// Create StreamProcessor
	processor := stream.NewStreamProcessor(stream.StreamConfig{
		Source:            stream.NewRawSource(response.Body, 32*1024),
		Transform:         nil, // Passthrough: no transformation
		Writer:            ra.getStreamWriter(),
		Context:           ctx,
		FirstTokenTimeout: firstTokenTimeout,
		HeartbeatInterval: streamHeartbeatInterval(),
		BufferRawStream:   true,
		TerminalEvents:    cfg.TerminalEvents,
		OnFirstToken: func() {
			ra.metrics.SetFirstTokenTime(time.Now())
			ra.stopFirstTokenTimer()
		},
		OnFinish: func(ctx context.Context, rawStream []byte) error {
			if len(rawStream) == 0 {
				return stream.ErrEmptyUpstreamStream
			}
			// Copy to buffer for metrics collection
			rawStreamBuf.Write(rawStream)

			// Collect passthrough metrics
			ra.collectPassthroughMetrics(ctx, rawStream)

			// Collect response if configured
			if cfg.CollectMetrics {
				ra.collectResponse()
			}

			log.Debugf("passthrough stream end")
			return nil
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
		_ = response.Body.Close()
		return ra.firstTokenTimeoutError()
	}

	// Check for context cancellation with first token timeout
	if err != nil {
		if timeoutErr := ra.firstTokenTimeoutIfNeeded(ctx, err); timeoutErr != nil {
			return timeoutErr
		}
	}

	// On disconnect with partial data, still try to collect metrics
	if err != nil && errors.Is(err, context.Canceled) && rawStreamBuf.Len() > 0 {
		ra.collectPassthroughMetrics(context.Background(), rawStreamBuf.Bytes())
		if cfg.CollectMetrics {
			ra.collectResponse()
		}
	}

	return err
}

// collectPassthroughMetrics parses raw SSE stream for metrics aggregation without mutating response.
func (ra *relayAttempt) collectPassthroughMetrics(ctx context.Context, rawStream []byte) {
	if len(rawStream) == 0 {
		return
	}

	// Try stream event adapter first (preferred)
	outEventAdapter, outOk := ra.outAdapter.(model.OutboundStreamEventTransformer)
	inEventAdapter, inOk := ra.inAdapter.(model.InboundStreamEventTransformer)
	if outOk && inOk {
		readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
		for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
			if err != nil {
				log.Debugf("passthrough metrics parse skipped: %v", err)
				return
			}
			if events, terr := outEventAdapter.TransformStreamEvent(ctx, []byte(ev.Data)); terr == nil && len(events) > 0 {
				_, _ = inEventAdapter.TransformStreamEvents(ctx, events)
			}
		}
		return
	}

	// Fallback to traditional stream transformer
	readCfg := &sse.ReadConfig{MaxEventSize: maxSSEEventSize}
	for ev, err := range sse.Read(bytes.NewReader(rawStream), readCfg) {
		if err != nil {
			log.Debugf("passthrough metrics parse skipped: %v", err)
			return
		}
		if chunk, terr := ra.outAdapter.TransformStream(ctx, []byte(ev.Data)); terr == nil && chunk != nil {
			_, _ = ra.inAdapter.TransformStream(ctx, chunk)
		}
	}
}

// transformStreamData 转换流式数据
func (ra *relayAttempt) transformStreamData(ctx context.Context, data string) ([]byte, error) {
	events, ok, err := ra.decodeOutboundStreamEvents(ctx, []byte(data))
	if err != nil {
		log.Warnf("failed to transform stream events: %v", err)
		return nil, err
	}
	if ok {
		return ra.encodeInboundStreamEvents(ctx, events)
	}

	internalStream, err := ra.decodeOutboundStreamResponse(ctx, []byte(data))
	if err != nil {
		log.Warnf("failed to transform stream: %v", err)
		return nil, err
	}
	if internalStream == nil {
		return nil, nil
	}

	return ra.encodeInboundStreamResponse(ctx, internalStream)
}

func (ra *relayAttempt) decodeOutboundStreamEvents(ctx context.Context, data []byte) ([]model.StreamEvent, bool, error) {
	outEventAdapter, ok := ra.outAdapter.(model.OutboundStreamEventTransformer)
	if !ok {
		return nil, false, nil
	}
	if _, ok := ra.inAdapter.(model.InboundStreamEventTransformer); !ok {
		return nil, false, nil
	}
	events, err := outEventAdapter.TransformStreamEvent(ctx, data)
	if err != nil {
		return nil, true, err
	}
	return events, true, nil
}

func (ra *relayAttempt) encodeInboundStreamEvents(ctx context.Context, events []model.StreamEvent) ([]byte, error) {
	if len(events) == 0 {
		return nil, nil
	}
	inEventAdapter, ok := ra.inAdapter.(model.InboundStreamEventTransformer)
	if !ok {
		return nil, nil
	}
	inStream, err := inEventAdapter.TransformStreamEvents(ctx, events)
	if err != nil {
		log.Warnf("failed to transform inbound stream events: %v", err)
		return nil, err
	}
	return inStream, nil
}

func (ra *relayAttempt) decodeOutboundStreamResponse(ctx context.Context, data []byte) (*model.InternalLLMResponse, error) {
	return ra.outAdapter.TransformStream(ctx, data)
}

func (ra *relayAttempt) encodeInboundStreamResponse(ctx context.Context, internalStream *model.InternalLLMResponse) ([]byte, error) {
	inStream, err := ra.inAdapter.TransformStream(ctx, internalStream)
	if err != nil {
		log.Warnf("failed to transform stream: %v", err)
		return nil, err
	}
	return inStream, nil
}
