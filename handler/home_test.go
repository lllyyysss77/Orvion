package handler

import (
	"testing"
	"time"
)

func TestModelUsageRangeStart(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, time.August, 2, 16, 30, 0, 0, location) // Sunday
	tests := []struct {
		rangeName string
		want      time.Time
	}{
		{rangeName: "today", want: time.Date(2026, time.August, 2, 0, 0, 0, 0, location)},
		{rangeName: "week", want: time.Date(2026, time.July, 27, 0, 0, 0, 0, location)},
		{rangeName: "month", want: time.Date(2026, time.August, 1, 0, 0, 0, 0, location)},
	}
	for _, test := range tests {
		got, ok := modelUsageRangeStart(now, test.rangeName)
		if !ok || !got.Equal(test.want) {
			t.Fatalf("modelUsageRangeStart(%q)=(%v,%v), want %v", test.rangeName, got, ok, test.want)
		}
	}
	if _, ok := modelUsageRangeStart(now, "all"); ok {
		t.Fatal("不支持的范围应返回 false")
	}
}
