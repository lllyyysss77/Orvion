package providers

import (
	"crypto/sha256"
	"strings"
	"sync"
)

const maxAPIKeyRotationCounters = 1024

var apiKeyRotationCounters = struct {
	sync.Mutex
	values map[string]uint64
}{values: make(map[string]uint64)}

func splitProviderAPIKeys(raw string) []string {
	normalized := strings.NewReplacer("，", ",", "\n", ",", "\r", ",").Replace(raw)
	parts := strings.Split(normalized, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

func nextProviderAPIKey(scope string, raw string) string {
	keys := splitProviderAPIKeys(raw)
	if len(keys) == 0 {
		return strings.TrimSpace(raw)
	}
	if len(keys) == 1 {
		return keys[0]
	}

	// 缓存键不能保留原始 API Key；配置变更会生成新哈希，旧计数器会在容量到达上限时清理。
	counterKey := providerAPIKeyRotationKey(scope, keys)
	apiKeyRotationCounters.Lock()
	if len(apiKeyRotationCounters.values) >= maxAPIKeyRotationCounters {
		clear(apiKeyRotationCounters.values)
	}
	index := apiKeyRotationCounters.values[counterKey]
	apiKeyRotationCounters.values[counterKey] = index + 1
	apiKeyRotationCounters.Unlock()
	return keys[int(index%uint64(len(keys)))]
}

func providerAPIKeyRotationKey(scope string, keys []string) string {
	value := strings.TrimSpace(scope) + "\x00" + strings.Join(keys, "\x00")
	sum := sha256.Sum256([]byte(value))
	return string(sum[:])
}
