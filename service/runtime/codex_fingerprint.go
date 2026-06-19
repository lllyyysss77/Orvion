package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

var codexFingerprintHeaderPrefixes = []string{
	"x-stainless-",
}

var codexFingerprintHeaderNames = map[string]struct{}{
	"accept":                                {},
	"accept-language":                       {},
	"chatgpt-account-id":                    {},
	"connection":                            {},
	"openai-beta":                           {},
	"originator":                            {},
	"sec-fetch-mode":                        {},
	"session-id":                            {},
	"session_id":                            {},
	"user-agent":                            {},
	"version":                               {},
	"x-app":                                 {},
	"x-client-request-id":                   {},
	"x-codex-beta-features":                 {},
	"x-codex-turn-metadata":                 {},
	"x-codex-turn-state":                    {},
	"x-responsesapi-include-timing-metrics": {},
}

func LoadCodexFingerprintConfig(ctx context.Context) (models.CodexFingerprintConfig, bool) {
	config, err := gorm.G[models.Config](models.DB).
		Where("key = ?", models.KeyCodexFingerprint).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.CodexFingerprintConfig{}, false
		}
		slog.Error("读取 Codex 指纹模拟配置失败", "error", err)
		return models.CodexFingerprintConfig{}, false
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return models.CodexFingerprintConfig{}, false
	}

	var cfg models.CodexFingerprintConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		slog.Error("解析 Codex 指纹模拟配置失败", "error", err)
		return models.CodexFingerprintConfig{}, false
	}
	if !cfg.Enabled {
		return models.CodexFingerprintConfig{}, false
	}
	cfg.Headers = normalizeCodexFingerprintHeaders(cfg.Headers)
	if len(cfg.Headers) == 0 {
		return models.CodexFingerprintConfig{}, false
	}
	return cfg, true
}

func ApplyCodexFingerprintHeaders(header http.Header, cfg models.CodexFingerprintConfig, stream bool) http.Header {
	if header == nil {
		header = http.Header{}
	}
	if !cfg.Enabled {
		return header
	}
	headers := normalizeCodexFingerprintHeaders(cfg.Headers)
	if len(headers) == 0 {
		return header
	}
	removeCodexFingerprintHeaders(header)
	for key, value := range headers {
		header.Set(key, value)
	}
	if stream {
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	header.Set("Connection", "Keep-Alive")
	if strings.Contains(header.Get("User-Agent"), "Mac OS") {
		if sessionID := randomCodexSessionID(); sessionID != "" {
			header.Set("Session_id", sessionID)
		}
	}
	return header
}

func normalizeCodexFingerprintHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[http.CanonicalHeaderKey(key)] = value
	}
	return normalized
}

func removeCodexFingerprintHeaders(header http.Header) {
	for key := range header {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := codexFingerprintHeaderNames[normalized]; ok {
			header.Del(key)
			continue
		}
		for _, prefix := range codexFingerprintHeaderPrefixes {
			if strings.HasPrefix(normalized, prefix) {
				header.Del(key)
				break
			}
		}
	}
}

func randomCodexSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
