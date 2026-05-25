package service

import "testing"

func TestCalculateCacheHitRate(t *testing.T) {
	tests := []struct {
		name         string
		promptTokens int64
		cachedTokens int64
		want         float64
	}{
		{name: "正常命中率", promptTokens: 1000, cachedTokens: 250, want: 25},
		{name: "缓存数超过输入时封顶", promptTokens: 100, cachedTokens: 150, want: 100},
		{name: "没有输入 token", promptTokens: 0, cachedTokens: 50, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCacheHitRate(tt.promptTokens, tt.cachedTokens)
			if got != tt.want {
				t.Fatalf("calculateCacheHitRate(%d, %d) = %v, want %v", tt.promptTokens, tt.cachedTokens, got, tt.want)
			}
		})
	}
}
