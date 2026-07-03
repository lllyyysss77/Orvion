package agent

import (
	"context"
	"strings"

	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/models"
)

type telegramAgentFunctionToolHandler func(context.Context, int64, models.TelegramAgentConfig, telegramAgentToolCallArgs) string

type telegramAgentFunctionToolDefinition struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
	Category    string
	Handler     telegramAgentFunctionToolHandler
}

func telegramAgentFunctionToolDefinitions(ctx context.Context, cfg models.TelegramAgentConfig) []telegramAgentFunctionToolDefinition {
	specs := agenttools.FunctionDefinitions(ctx, cfg)
	definitions := make([]telegramAgentFunctionToolDefinition, 0, len(specs))
	for _, spec := range specs {
		spec := spec
		definitions = append(definitions, telegramAgentFunctionToolDefinition{
			Name:        spec.Name,
			Description: spec.Description,
			Properties:  spec.Properties,
			Required:    spec.Required,
			Category:    spec.Category,
			Handler: func(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) string {
				return agenttools.ExecuteFunctionTool(ctx, telegramAgentToolRuntime(), chatID, cfg, spec.Name, args)
			},
		})
	}
	return definitions
}

func findTelegramAgentFunctionToolDefinition(ctx context.Context, cfg models.TelegramAgentConfig, name string) (telegramAgentFunctionToolDefinition, bool) {
	name = strings.TrimSpace(name)
	for _, tool := range telegramAgentFunctionToolDefinitions(ctx, cfg) {
		if tool.Name == name {
			return tool, true
		}
	}
	return telegramAgentFunctionToolDefinition{}, false
}

func telegramAgentSkillMetadataPrompt(ctx context.Context, cfg models.TelegramAgentConfig) string {
	return agenttools.SkillMetadataPrompt(ctx, cfg)
}

func telegramAgentToolRuntime() agenttools.Runtime {
	return agenttools.Runtime{
		ResolveConversationID: func(ctx context.Context, chatID int64) string {
			conversationID, err := resolveTelegramActiveConversationID(ctx, chatID, getTelegramSession(chatID))
			if err != nil {
				return ""
			}
			return conversationID
		},
		RunScheduledTask: runTelegramAgentScheduledTaskFromTool,
	}
}
