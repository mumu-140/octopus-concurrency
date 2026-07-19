package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/relay/bodycache"
	"github.com/bestruirui/octopus/internal/utils/log"
)

type imagesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type imagesRelayMetrics struct {
	APIKeyID     int
	RequestModel string
	ActualModel  string
	StartTime    time.Time
	FirstToken   time.Time

	Stats model.StatsMetrics

	RequestContent  string
	ResponseContent string
}

func newImagesRelayMetrics(apiKeyID int, requestModel string) *imagesRelayMetrics {
	return &imagesRelayMetrics{
		APIKeyID:     apiKeyID,
		RequestModel: requestModel,
		StartTime:    time.Now(),
	}
}

func (m *imagesRelayMetrics) SetFirstTokenTime(t time.Time) {
	if m.FirstToken.IsZero() {
		m.FirstToken = t
	}
}

func (m *imagesRelayMetrics) SetUsageFromImages(actualModel string, u imagesUsage) {
	m.ActualModel = actualModel
	m.Stats.InputToken = int64(u.InputTokens)
	m.Stats.OutputToken = int64(u.OutputTokens)

	modelPrice := price.ResolveLLMPrice(actualModel, m.RequestModel)
	if modelPrice == nil {
		return
	}

	m.Stats.InputCost = float64(u.InputTokens) * modelPrice.Input * 1e-6
	m.Stats.OutputCost = float64(u.OutputTokens) * modelPrice.Output * 1e-6
}

func (m *imagesRelayMetrics) Save(ctx context.Context, success bool, err error, attempts []model.ChannelAttempt) {
	m.SaveWithChannelStats(ctx, success, err, attempts, true)
}

func (m *imagesRelayMetrics) SaveWithChannelStats(ctx context.Context, success bool, err error, attempts []model.ChannelAttempt, updateChannelStats bool) {
	duration := time.Since(m.StartTime)

	globalStats := model.StatsMetrics{
		WaitTime:    duration.Milliseconds(),
		InputToken:  m.Stats.InputToken,
		OutputToken: m.Stats.OutputToken,
		InputCost:   m.Stats.InputCost,
		OutputCost:  m.Stats.OutputCost,
	}
	if success {
		globalStats.RequestSuccess = 1
	} else {
		globalStats.RequestFailed = 1
	}

	channelID, channelName := finalChannel(attempts)
	op.StatsTotalUpdate(globalStats)
	op.StatsHourlyUpdate(globalStats)
	op.StatsDailyUpdate(context.Background(), globalStats)
	op.StatsAPIKeyUpdate(m.APIKeyID, globalStats)
	if updateChannelStats {
		op.StatsChannelUpdate(channelID, globalStats)
	} else {
		updateFinalChannelUsageStats(channelID, globalStats)
	}
	op.StatsSiteModelHourlyRecordAttempts(attempts, m.ActualModel)

	if conf.AppConfig.Log.Relay.Summary || !success {
		fields := []interface{}{
			"model", m.RequestModel,
			"actual_model", m.ActualModel,
			"channel_id", channelID,
			"channel", channelName,
			"success", success,
			"duration_ms", duration.Milliseconds(),
			"input_token", m.Stats.InputToken,
			"output_token", m.Stats.OutputToken,
			"input_cost", m.Stats.InputCost,
			"output_cost", m.Stats.OutputCost,
			"total_cost", m.Stats.InputCost + m.Stats.OutputCost,
			"attempts", len(attempts),
		}
		if success {
			log.Infow("relay.images.complete", fields...)
		} else {
			log.Warnw("relay.images.complete", fields...)
		}
	}

	m.saveLog(ctx, success, err, duration, attempts, channelID, channelName)
}

func (m *imagesRelayMetrics) saveLog(ctx context.Context, success bool, err error, duration time.Duration, attempts []model.ChannelAttempt, channelID int, channelName string) {
	actualModel := m.ActualModel
	if actualModel == "" {
		actualModel = m.RequestModel
	}

	relayLog := model.RelayLog{
		Time:             m.StartTime.Unix(),
		RequestModelName: m.RequestModel,
		ChannelName:      channelName,
		ChannelId:        channelID,
		ActualModelName:  actualModel,
		UseTime:          int(duration.Milliseconds()),
		Attempts:         attempts,
		TotalAttempts:    len(attempts),
		RequestContent:   m.RequestContent,
		ResponseContent:  m.ResponseContent,
	}

	if apiKey, getErr := op.APIKeyGet(m.APIKeyID, ctx); getErr == nil {
		relayLog.RequestAPIKeyName = apiKey.Name
	}

	// 首字时间
	if !m.FirstToken.IsZero() {
		relayLog.Ftut = int(m.FirstToken.Sub(m.StartTime).Milliseconds())
	}

	// Usage
	if m.Stats.InputToken > 0 || m.Stats.OutputToken > 0 {
		relayLog.InputTokens = int(m.Stats.InputToken)
		relayLog.OutputTokens = int(m.Stats.OutputToken)
		relayLog.Cost = m.Stats.InputCost + m.Stats.OutputCost
	}

	if err != nil {
		relayLog.Error = err.Error()
	}
	relayLog.Success = success

	if logErr := op.RelayLogAdd(ctx, relayLog); logErr != nil {
		log.Warnf("failed to save relay log: %v", logErr)
	}
}

func buildImagesRequestContentForLog(isMultipart bool, bc *bodycache.BodyCache, jsonPayload map[string]any) string {
	if isMultipart {
		// multipart 可能包含图片文件，避免落库
		return fmt.Sprintf(`{"content_type":"multipart/form-data","size_bytes":%d,"note":"multipart request content omitted for storage"}`, bc.Size())
	}
	if jsonPayload == nil {
		return ""
	}
	b, err := json.Marshal(jsonPayload)
	if err != nil {
		return ""
	}
	return truncateString(string(b), 8*1024)
}

func buildImagesResponseContentForLog(stream bool, upstreamCT string, usage *imagesUsage) string {
	if usage == nil {
		return ""
	}
	// 不记录 b64_json，仅记录 usage
	type respForLog struct {
		Stream      bool         `json:"stream"`
		ContentType string       `json:"content_type,omitempty"`
		Usage       *imagesUsage `json:"usage,omitempty"`
		Note        string       `json:"note,omitempty"`
	}
	obj := respForLog{
		Stream:      stream,
		ContentType: upstreamCT,
		Usage:       usage,
		Note:        "image data omitted for storage",
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return string(b)
}

func truncateString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
