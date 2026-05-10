package ifacebridge

import (
	"context"

	"github.com/racio/orvion/consts"
)

func ApplyUpstreamContext(ctx context.Context, plan Plan) context.Context {
	if !plan.Enabled {
		return ctx
	}
	if endpoint := plan.UpstreamOpenAIEndpoint(); endpoint != "" {
		return context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, endpoint)
	}
	return ctx
}
