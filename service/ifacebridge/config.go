package ifacebridge

import (
	"strings"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
)

const (
	EndpointChat      = "chat"
	EndpointResponses = "responses"
	EndpointMessages  = "messages"
)

type Plan struct {
	Enabled          bool
	ClientEndpoint   string
	UpstreamEndpoint string
}

func (p Plan) UpstreamStyle() string {
	switch p.UpstreamEndpoint {
	case EndpointChat:
		return consts.StyleOpenAI
	case EndpointResponses:
		return consts.StyleOpenAIRes
	case EndpointMessages:
		return consts.StyleAnthropic
	default:
		return ""
	}
}

func (p Plan) UpstreamOpenAIEndpoint() string {
	switch p.UpstreamEndpoint {
	case EndpointChat:
		return "chat/completions"
	case EndpointResponses:
		return "responses"
	default:
		return ""
	}
}

func ResolvePlan(provider models.Provider, clientEndpoint string) (Plan, bool) {
	clientEndpoint = NormalizeEndpoint(clientEndpoint)
	if clientEndpoint == "" {
		return Plan{}, false
	}

	capabilities := []string(provider.Capabilities)
	if models.ProviderSupportsEndpoint(capabilities, clientEndpoint) {
		return Plan{}, false
	}

	if provider.InterfaceConversionEnabled != 1 {
		return Plan{}, false
	}

	target := NormalizeEndpoint(provider.InterfaceConversionTarget)
	if target == "" || target == clientEndpoint {
		return Plan{}, false
	}
	if !models.ProviderSupportsEndpoint(capabilities, target) {
		return Plan{}, false
	}
	if !SupportsConversion(clientEndpoint, target) {
		return Plan{}, false
	}

	return Plan{
		Enabled:          true,
		ClientEndpoint:   clientEndpoint,
		UpstreamEndpoint: target,
	}, true
}

func NormalizeEndpoint(endpoint string) string {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "chat", "chat/completions", "chat_completions":
		return EndpointChat
	case "responses", "response":
		return EndpointResponses
	case "messages", "message", "claude":
		return EndpointMessages
	default:
		return ""
	}
}

func SupportsConversion(fromEndpoint string, toEndpoint string) bool {
	from := NormalizeEndpoint(fromEndpoint)
	to := NormalizeEndpoint(toEndpoint)
	if from == "" || to == "" || from == to {
		return false
	}
	return true
}
