package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

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
