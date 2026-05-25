package providers

import (
	"strings"
	"sync"
	"sync/atomic"
)

var apiKeyRotationCounters sync.Map

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

	counterKey := strings.TrimSpace(scope) + "\x00" + strings.Join(keys, "\x00")
	value, _ := apiKeyRotationCounters.LoadOrStore(counterKey, &atomic.Uint64{})
	counter := value.(*atomic.Uint64)
	index := counter.Add(1) - 1
	return keys[int(index%uint64(len(keys)))]
}
