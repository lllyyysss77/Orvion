package providers

import (
	"sync"
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
	apiKeyRotationCounters = sync.Map{}

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

func TestNextProviderAPIKeySingleKey(t *testing.T) {
	if got := nextProviderAPIKey("scope", "  only-key  "); got != "only-key" {
		t.Fatalf("单 Key 应去除空白后原样返回: got=%q", got)
	}
}
