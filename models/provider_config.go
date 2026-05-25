package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func NormalizeProviderConfig(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("provider config is required")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return "", err
	}
	normalized := make(map[string]string, len(parsed))
	for key, value := range parsed {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			return "", errors.New("provider config contains empty key")
		}
		if _, exists := normalized[normalizedKey]; exists {
			return "", fmt.Errorf("provider config contains duplicate key: %s", normalizedKey)
		}
		normalized[normalizedKey] = providerConfigValueToString(value)
	}

	data, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func providerConfigValueToString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}
