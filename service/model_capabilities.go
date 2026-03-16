package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	ModelCapabilityChat      = "chat"
	ModelCapabilityVision    = "vision"
	ModelCapabilityVideo     = "video"
	ModelCapabilityEmbedding = "embedding"
	ModelCapabilityRerank    = "rerank"
)

type ModelEndpointRule struct {
	Required []string
	Suffix   string
	Label    string
}

var defaultModelCapabilities = []string{ModelCapabilityChat}

var modelEndpointRules = map[string]ModelEndpointRule{
	"chat": {
		Required: []string{ModelCapabilityChat},
		Suffix:   "-chat",
		Label:    "对话",
	},
	"responses": {
		Required: []string{ModelCapabilityChat, ModelCapabilityVision},
		Suffix:   "-vision",
		Label:    "对话+视觉",
	},
	"messages": {
		Required: []string{ModelCapabilityChat, ModelCapabilityVision},
		Suffix:   "-vision",
		Label:    "对话+视觉",
	},
	"images": {
		Required: []string{ModelCapabilityVision},
		Suffix:   "-vision",
		Label:    "视觉",
	},
	"videos": {
		Required: []string{ModelCapabilityVideo},
		Suffix:   "-video",
		Label:    "视频",
	},
	"embeddings": {
		Required: []string{ModelCapabilityEmbedding},
		Suffix:   "-embedding",
		Label:    "嵌入",
	},
}

func ValidateModelCapability(ctx context.Context, modelName string, endpoint string) error {
	rule, ok := modelEndpointRules[endpoint]
	if !ok {
		return nil
	}

	model, err := gorm.G[models.Model](models.DB).Where("name = ?", modelName).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("not found model %s", modelName)
		}
		return err
	}

	caps := normalizeModelCapabilities(model.Capabilities)
	if len(caps) == 0 {
		caps = normalizeModelCapabilities(defaultModelCapabilities)
	}

	if !hasAllCapabilities(caps, rule.Required) {
		return fmt.Errorf("模型不支持此接口调用方式，请使用%s后缀进行调用", rule.Suffix)
	}

	return nil
}

func normalizeModelCapabilities(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		switch value {
		case "embeddings", "embed":
			value = ModelCapabilityEmbedding
		}
		out[value] = struct{}{}
	}
	return out
}

func hasAllCapabilities(have map[string]struct{}, required []string) bool {
	for _, raw := range required {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		if _, ok := have[value]; !ok {
			return false
		}
	}
	return true
}
