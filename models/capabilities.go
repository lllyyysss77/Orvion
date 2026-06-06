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

// ModelCapabilities 兼容旧数据与 JSON 数组的模型能力字段。
// 允许数据库中存在 "chat" 或 "chat,vision" 等旧格式。
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
	if strings.HasPrefix(trimmed, "[") {
		var parsed []string
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			*m = sanitizeCapabilities([]string{trimmed})
			return nil
		}
		*m = sanitizeCapabilities(parsed)
		return nil
	}
	parts := strings.Split(trimmed, ",")
	*m = sanitizeCapabilities(parts)
	return nil
}

func sanitizeCapabilities(values []string) ModelCapabilities {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		switch value {
		case "embeddings", "embed":
			value = "embedding"
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// ProviderCapabilities 提供商支持的接口能力。
// 允许数据库中存在 "chat,openai" 或 JSON 数组格式。
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
	if strings.HasPrefix(trimmed, "[") {
		var parsed []string
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			*p = sanitizeProviderCapabilities([]string{trimmed})
			return nil
		}
		*p = sanitizeProviderCapabilities(parsed)
		return nil
	}
	parts := strings.Split(trimmed, ",")
	*p = sanitizeProviderCapabilities(parts)
	return nil
}

func NormalizeProviderCapabilities(values []string) ProviderCapabilities {
	caps := sanitizeProviderCapabilities(values)
	if len(caps) == 0 {
		return append(ProviderCapabilities{}, defaultProviderCapabilities...)
	}
	return caps
}

func sanitizeProviderCapabilities(values []string) ProviderCapabilities {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "chat", "chat_completions", "completions":
			value = ProviderCapabilityChat
		case "openai", "responses", "response":
			value = ProviderCapabilityOpenAI
		case "claude", "anthropic", "messages", "message":
			value = ProviderCapabilityClaude
		default:
			value = ""
		}
		if value == "" {
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
	case "chat", "chat/completions", "chat_completions", "embeddings", "rerank", "images", "videos":
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
