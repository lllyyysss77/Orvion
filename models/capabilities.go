package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ProviderCapabilityChat   = "chat"
	ProviderCapabilityOpenAI = "openai"
	ProviderCapabilityClaude = "claude"
)

var defaultProviderCapabilities = ProviderCapabilities{
	ProviderCapabilityChat,
	ProviderCapabilityOpenAI,
	ProviderCapabilityClaude,
}

// ModelCapabilities 模型能力字段，数据库中使用 JSON 数组保存。
type ModelCapabilities []string

func (m *ModelCapabilities) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*m = nil
		return nil
	case []byte:
		return m.parse(string(v))
	case string:
		return m.parse(v)
	default:
		return fmt.Errorf("unsupported capabilities type: %T", value)
	}
}

func (m ModelCapabilities) Value() (driver.Value, error) {
	if len(m) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal([]string(m))
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (m *ModelCapabilities) parse(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		*m = nil
		return nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return fmt.Errorf("capabilities must be JSON array")
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return err
	}
	*m = sanitizeCapabilities(parsed)
	return nil
}

func sanitizeCapabilities(values []string) ModelCapabilities {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "chat", "vision", "embedding", "rerank":
		default:
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// ProviderCapabilities 提供商支持的接口能力，数据库中使用 JSON 数组保存。
type ProviderCapabilities []string

func (p *ProviderCapabilities) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*p = nil
		return nil
	case []byte:
		return p.parse(string(v))
	case string:
		return p.parse(v)
	default:
		return fmt.Errorf("unsupported provider capabilities type: %T", value)
	}
}

func (p ProviderCapabilities) Value() (driver.Value, error) {
	if len(p) == 0 {
		return "[]", nil
	}
	data, err := json.Marshal([]string(p))
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (p *ProviderCapabilities) parse(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		*p = nil
		return nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return fmt.Errorf("provider capabilities must be JSON array")
	}
	var parsed []string
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return err
	}
	*p = sanitizeProviderCapabilities(parsed)
	return nil
}

func NormalizeProviderCapabilities(values []string) ProviderCapabilities {
	if len(values) == 0 {
		return append(ProviderCapabilities{}, defaultProviderCapabilities...)
	}
	caps := sanitizeProviderCapabilities(values)
	return caps
}

func sanitizeProviderCapabilities(values []string) ProviderCapabilities {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case ProviderCapabilityChat, ProviderCapabilityOpenAI, ProviderCapabilityClaude:
		default:
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func ResolveRequiredProviderCapability(endpoint string) string {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "chat", "chat/completions", "chat_completions", "embeddings", "rerank", "images":
		return ProviderCapabilityChat
	case "responses":
		return ProviderCapabilityOpenAI
	case "messages":
		return ProviderCapabilityClaude
	default:
		return ""
	}
}

func ProviderSupportsEndpoint(values []string, endpoint string) bool {
	required := ResolveRequiredProviderCapability(endpoint)
	if required == "" {
		return true
	}
	caps := NormalizeProviderCapabilities(values)
	for _, cap := range caps {
		if cap == required {
			return true
		}
	}
	return false
}
