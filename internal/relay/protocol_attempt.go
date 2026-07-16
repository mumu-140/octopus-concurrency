package relay

import (
	"fmt"
	"net/http"
	"strings"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocol"
	"github.com/bestruirui/octopus/internal/protocolroute"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
)

var adaptiveBlockedClientHeaders = map[string]bool{
	"cookie":              true,
	"set-cookie":          true,
	"api-key":             true,
	"anthropic-api-key":   true,
	"x-goog-api-key":      true,
	"anthropic-version":   true,
	"openai-organization": true,
	"openai-project":      true,
}

var adapterCredentialHeaders = []string{
	"Authorization",
	"X-Api-Key",
	"Api-Key",
	"Anthropic-Api-Key",
	"X-Goog-Api-Key",
}

func (ra *relayAttempt) effectiveUpstreamProtocol() protocol.Protocol {
	if ra == nil {
		return protocol.Unknown
	}
	if ra.upstreamProtocol.Valid() {
		return ra.upstreamProtocol
	}
	if ra.plan != nil {
		return ra.plan.UpstreamProtocol()
	}
	if ra.channel != nil {
		return protocol.FromOutboundType(ra.channel.Type)
	}
	return protocol.Unknown
}

func (ra *relayAttempt) effectiveBaseURL() string {
	if ra != nil && ra.plan != nil {
		return ra.plan.BaseURL()
	}
	if ra != nil && ra.channel != nil {
		return ra.channel.GetBaseUrl()
	}
	return ""
}

func (ra *relayAttempt) effectiveParamOverride() *string {
	if ra != nil && ra.plan != nil {
		value := ra.plan.ParamOverride()
		if len(value) == 0 {
			return nil
		}
		text := string(value)
		return &text
	}
	if ra != nil && ra.channel != nil {
		return ra.channel.ParamOverride
	}
	return nil
}

func (ra *relayAttempt) effectiveHeaders() map[string]string {
	if ra != nil && ra.plan != nil {
		return ra.plan.HeaderPolicy().Set
	}
	if ra != nil && ra.channel != nil {
		return customHeaderMap(ra.channel.CustomHeader)
	}
	return nil
}

func (ra *relayAttempt) adaptiveHeaderIsolationEnabled() bool {
	return ra != nil && ra.plan != nil && !ra.plan.IsLegacyFixed()
}

func (ra *relayAttempt) allowClientHeader(name string) bool {
	if !ra.adaptiveHeaderIsolationEnabled() {
		return true
	}
	lowerName := strings.ToLower(name)
	if adaptiveBlockedClientHeaders[lowerName] {
		return false
	}
	if lowerName == "anthropic-beta" {
		return ra.plan.ConversionMode() == protocolroute.ModeRawPassthrough &&
			ra.effectiveUpstreamProtocol() == protocol.Anthropic
	}
	return true
}

func captureAdapterCredentials(header http.Header) http.Header {
	captured := make(http.Header)
	for _, name := range adapterCredentialHeaders {
		if values := header.Values(name); len(values) > 0 {
			captured[name] = append([]string(nil), values...)
		}
	}
	return captured
}

func restoreAdapterCredentials(header, captured http.Header) {
	for _, name := range adapterCredentialHeaders {
		header.Del(name)
		for _, value := range captured.Values(name) {
			header.Add(name, value)
		}
	}
}

type relayPlanInput struct {
	Snapshot       protocolroute.PolicySnapshot
	Mode           protocolroute.RoutingMode
	ChannelID      int
	ChannelKeyID   int
	ChannelType    outbound.OutboundType
	RequestedModel string
	UpstreamModel  string
	Request        *transformerModel.InternalLLMRequest
	LegacyConfig   protocolroute.AttemptConfig
	Profiles       map[protocol.Protocol]protocolroute.AttemptConfig
	LegacyEligible bool
}

func resolveRelayAttemptPlan(input relayPlanInput) protocolroute.Decision {
	resolveInput := protocolroute.ResolveInput{
		Snapshot:         input.Snapshot,
		ChannelID:        input.ChannelID,
		ChannelKeyID:     input.ChannelKeyID,
		ChannelType:      protocol.FromOutboundType(input.ChannelType),
		RequestedModel:   input.RequestedModel,
		UpstreamModel:    input.UpstreamModel,
		Ingress:          protocol.FromAPIFormat(input.Request.RawAPIFormat),
		Features:         protocolroute.CaptureFeatures(input.Request),
		BaseURL:          input.LegacyConfig.BaseURL,
		HeaderPolicy:     input.LegacyConfig.HeaderPolicy,
		ParamOverride:    input.LegacyConfig.ParamOverride,
		AdaptiveProfiles: input.Profiles,
		LegacyEligible:   input.LegacyEligible,
	}
	if input.Mode == protocolroute.RoutingAdaptive {
		return protocolroute.ResolvePrimary(resolveInput)
	}
	if input.Mode == protocolroute.RoutingObserve {
		protocolroute.ObserveShadowDecisionNow(resolveInput)
	}
	return protocolroute.Decision{Plan: protocolroute.NewLegacyFixedAttempt(protocolroute.PlanSpec{
		ChannelID:        input.ChannelID,
		ChannelKeyID:     input.ChannelKeyID,
		RequestedModel:   input.RequestedModel,
		UpstreamModel:    input.UpstreamModel,
		IngressProtocol:  resolveInput.Ingress,
		UpstreamProtocol: resolveInput.ChannelType,
		Features:         resolveInput.Features,
		BaseURL:          input.LegacyConfig.BaseURL,
		HeaderPolicy:     input.LegacyConfig.HeaderPolicy,
		ParamOverride:    input.LegacyConfig.ParamOverride,
		PolicySource:     "legacy_fixed",
		ConfigRevision:   input.Snapshot.ConfigRevision,
	})}
}

func newRelayAttempt(request *relayRequest, channel *dbmodel.Channel, key dbmodel.ChannelKey, plan *protocolroute.AttemptPlan, firstTokenTimeout int) (*relayAttempt, error) {
	if plan == nil {
		return nil, fmt.Errorf("protocol attempt plan is nil")
	}
	outboundType, ok := plan.UpstreamProtocol().ToOutboundType()
	if !ok {
		return nil, fmt.Errorf("unsupported upstream protocol: %s", plan.UpstreamProtocol())
	}
	adapter := outbound.Get(outboundType)
	if adapter == nil {
		return nil, fmt.Errorf("no adapter registered for upstream protocol: %s", plan.UpstreamProtocol())
	}
	attemptRequest := *request
	attemptRequest.internalRequest = request.internalRequest.Clone()
	attemptRequest.internalRequest.Model = plan.UpstreamModel()
	return &relayAttempt{
		relayRequest:         &attemptRequest,
		outAdapter:           adapter,
		channel:              channel,
		usedKey:              key,
		plan:                 plan,
		upstreamProtocol:     plan.UpstreamProtocol(),
		firstTokenTimeOutSec: firstTokenTimeout,
	}, nil
}
