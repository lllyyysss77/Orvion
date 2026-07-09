package providers

import (
	"fmt"
	"testing"
)

func TestSplitProviderAPIKeys(t *testing.T) {
	keys := splitProviderAPIKeys(" key-a, key-b，key-c\nkey-d ,, ")
	want := []string{"key-a", "key-b", "key-c", "key-d"}
	if len(keys) != len(want) {
		t.Fatalf("Key 数量异常: got=%d want=%d", len(keys), len(want))
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("第 %d 个 Key 异常: got=%q want=%q", i, keys[i], want[i])
		}
	}
}

func TestNextProviderAPIKeyRotation(t *testing.T) {
	resetAPIKeyRotationCountersForTest()

	raw := "key-a,key-b,key-c"
	scope := "https://api.example.com"
	got := []string{
		nextProviderAPIKey(scope, raw),
		nextProviderAPIKey(scope, raw),
		nextProviderAPIKey(scope, raw),
		nextProviderAPIKey(scope, raw),
	}
	want := []string{"key-a", "key-b", "key-c", "key-a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 次轮询异常: got=%q want=%q", i+1, got[i], want[i])
		}
	}
}

func TestProviderAPIKeyRotationKeyDoesNotContainRawKey(t *testing.T) {
	key := providerAPIKeyRotationKey("https://api.example.com", []string{"secret-key"})
	if key == "" || len(key) == len("secret-key") {
		t.Fatalf("轮询缓存键应为固定长度哈希")
	}
	if key == "secret-key" {
		t.Fatalf("轮询缓存键不应暴露原始 API Key")
	}
}

func TestProviderAPIKeyRotationCountersStayBounded(t *testing.T) {
	resetAPIKeyRotationCountersForTest()
	for index := 0; index <= maxAPIKeyRotationCounters; index++ {
		nextProviderAPIKey(fmt.Sprintf("https://api-%d.example.com", index), "key-a,key-b")
	}

	apiKeyRotationCounters.Lock()
	defer apiKeyRotationCounters.Unlock()
	if got := len(apiKeyRotationCounters.values); got > maxAPIKeyRotationCounters {
		t.Fatalf("轮询计数器数量超出上限: got=%d max=%d", got, maxAPIKeyRotationCounters)
	}
}

func resetAPIKeyRotationCountersForTest() {
	apiKeyRotationCounters.Lock()
	apiKeyRotationCounters.values = make(map[string]uint64)
	apiKeyRotationCounters.Unlock()
}

func TestNextProviderAPIKeySingleKey(t *testing.T) {
	if got := nextProviderAPIKey("scope", "  only-key  "); got != "only-key" {
		t.Fatalf("单 Key 应去除空白后原样返回: got=%q", got)
	}
}
