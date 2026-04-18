package service

import (
	"testing"
	"time"
)

func TestShouldRunTelegramDailyUsageReport(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)

	cases := []struct {
		name     string
		now      time.Time
		lastSent string
		wantRun  bool
	}{
		{
			name:     "未到触发时间不执行",
			now:      time.Date(2026, 4, 18, 8, 59, 59, 0, loc),
			lastSent: "",
			wantRun:  false,
		},
		{
			name:     "到达九点执行",
			now:      time.Date(2026, 4, 18, 9, 0, 0, 0, loc),
			lastSent: "",
			wantRun:  true,
		},
		{
			name:     "同一天已发送不重复执行",
			now:      time.Date(2026, 4, 18, 10, 0, 0, 0, loc),
			lastSent: "2026-04-18",
			wantRun:  false,
		},
		{
			name:     "跨天后可再次执行",
			now:      time.Date(2026, 4, 19, 9, 1, 0, 0, loc),
			lastSent: "2026-04-18",
			wantRun:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, gotRun := shouldRunTelegramDailyUsageReport(tc.now, tc.lastSent)
			if gotRun != tc.wantRun {
				t.Fatalf("got run=%v, want=%v", gotRun, tc.wantRun)
			}
		})
	}
}

func TestResolveTelegramYesterdayRange(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 4, 18, 15, 30, 0, 0, loc)

	start, end := resolveTelegramYesterdayRange(now)
	if start.Format("2006-01-02 15:04:05") != "2026-04-17 00:00:00" {
		t.Fatalf("unexpected start: %s", start.Format("2006-01-02 15:04:05"))
	}
	if end.Format("2006-01-02 15:04:05") != "2026-04-18 00:00:00" {
		t.Fatalf("unexpected end: %s", end.Format("2006-01-02 15:04:05"))
	}
}
