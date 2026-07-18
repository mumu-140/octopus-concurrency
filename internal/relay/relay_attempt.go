package relay

import (
	"fmt"
	"net/http"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay/balancer"
)

func circuitFailureKind(retryEnabled bool, statusCode int) balancer.FailureKind {
	if retryEnabled && isPassthroughStatus(statusCode) {
		return balancer.FailureSoftRateLimit
	}
	return balancer.FailureHard
}

// attempt 统一管理一次通道尝试的完整生命周期
func (ra *relayAttempt) attempt() attemptResult {
	span := ra.iter.StartAttempt(ra.channel.ID, ra.usedKey.ID, ra.channel.Name)
	if ra.plan != nil {
		span.SetProtocolDecision(
			string(ra.plan.GroupProtocolMode()),
			string(ra.plan.IngressProtocol()),
			string(ra.plan.UpstreamProtocol()),
			string(ra.plan.AttemptKind()),
			ra.plan.FallbackReason(),
		)
	}
	statusCode, fwdErr := ra.forward()
	ra.usedKey.StatusCode = statusCode
	ra.usedKey.LastUseTimeStamp = time.Now().Unix()

	if fwdErr == nil {
		return ra.finishSuccessfulAttempt(span, statusCode)
	}
	if isClientCancellation(ra.requestContext(), fwdErr) {
		return ra.finishCanceledAttempt(span, statusCode, fwdErr)
	}
	return ra.finishFailedAttempt(span, statusCode, fwdErr)
}

func (ra *relayAttempt) finishSuccessfulAttempt(span *balancer.AttemptSpan, statusCode int) attemptResult {
	ra.collectResponse()
	ra.usedKey.TotalCost += ra.metrics.Stats.InputCost + ra.metrics.Stats.OutputCost
	op.ChannelKeyUpdate(ra.usedKey)
	span.End(dbmodel.AttemptSuccess, statusCode, "")
	op.StatsChannelUpdate(ra.channel.ID, dbmodel.StatsMetrics{
		WaitTime: span.Duration().Milliseconds(), RequestSuccess: 1,
	})
	balancer.RecordSuccess(ra.channel.ID, ra.usedKey.ID, ra.internalRequest.Model)
	balancer.SetSticky(ra.apiKeyID, ra.requestModel, ra.channel.ID, ra.usedKey.ID)
	return attemptResult{Success: true}
}

func (ra *relayAttempt) finishCanceledAttempt(span *balancer.AttemptSpan, statusCode int, fwdErr error) attemptResult {
	written := ra.deliveryStarted()
	if written {
		ra.collectResponse()
	}
	op.ChannelKeyUpdate(ra.usedKey)
	span.End(dbmodel.AttemptFailed, statusCode, fwdErr.Error())
	return attemptResult{
		Written: written, Canceled: true, Err: fwdErr, StatusCode: statusCode,
		UpstreamErrorBody: ra.upstreamErrorBody, UpstreamStatus: ra.upstreamStatusCode,
		UpstreamStarted: ra.upstreamStarted,
	}
}

func (ra *relayAttempt) finishFailedAttempt(span *balancer.AttemptSpan, statusCode int, fwdErr error) attemptResult {
	op.ChannelKeyUpdate(ra.usedKey)
	span.End(dbmodel.AttemptFailed, statusCode, fwdErr.Error())
	op.StatsChannelUpdate(ra.channel.ID, dbmodel.StatsMetrics{
		WaitTime:      span.Duration().Milliseconds(),
		RequestFailed: 1,
	})
	written := ra.deliveryStarted()
	if written {
		ra.collectResponse()
	}
	firstTokenTimeout := isFirstTokenTimeout(nil, fwdErr)
	return attemptResult{
		Success:           false,
		Written:           written,
		ResetConversation: statusCode == http.StatusConflict && needsConversationRestart(relayErrorMessage(fwdErr)),
		FirstTokenTimeout: firstTokenTimeout,
		Err:               fmt.Errorf("channel %s failed: %v", ra.channel.Name, fwdErr),
		StatusCode:        statusCode,
		RetryAfter:        ra.retryAfter,
		UpstreamErrorBody: ra.upstreamErrorBody,
		UpstreamStatus:    ra.upstreamStatusCode,
		UpstreamStarted:   ra.upstreamStarted,
	}
}

func (ra *relayAttempt) deliveryStarted() bool {
	if ra.streamPayloadWritten.Load() {
		return true
	}
	if ra.streamWriter != nil {
		return ra.streamWriter.Written()
	}
	return ra.c != nil && ra.c.Writer.Written()
}
