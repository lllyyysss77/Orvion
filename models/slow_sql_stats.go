package models

import (
	"context"
	"sync"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

const slowSQLStatsWindowSize = 10_000

type SlowSQLStatsSnapshot struct {
	TotalQueries uint64  `json:"total_queries"`
	SlowQueries  uint64  `json:"slow_queries"`
	NormalRate   float64 `json:"normal_rate"`
	SlowRate     float64 `json:"slow_rate"`
	ThresholdMs  int64   `json:"threshold_ms"`
	WindowSize   uint64  `json:"window_size"`
}

type slowSQLStatsLogger struct {
	gormlogger.Interface
	threshold time.Duration
}

var (
	slowSQLWindowMu      sync.Mutex
	slowSQLWindow        [slowSQLStatsWindowSize]bool
	slowSQLWindowCursor  uint64
	slowSQLWindowSamples uint64
	slowSQLWindowSlow    uint64
)

func newSlowSQLStatsLogger(base gormlogger.Interface, threshold time.Duration) gormlogger.Interface {
	return slowSQLStatsLogger{
		Interface: base,
		threshold: threshold,
	}
}

func (l slowSQLStatsLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return slowSQLStatsLogger{
		Interface: l.Interface.LogMode(level),
		threshold: l.threshold,
	}
}

func (l slowSQLStatsLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	recordSQLTrace(time.Since(begin), l.threshold)
	l.Interface.Trace(ctx, begin, fc, err)
}

func recordSQLTrace(elapsed time.Duration, threshold time.Duration) {
	isSlow := threshold > 0 && elapsed > threshold

	slowSQLWindowMu.Lock()
	defer slowSQLWindowMu.Unlock()

	index := slowSQLWindowCursor % slowSQLStatsWindowSize
	if slowSQLWindowSamples >= slowSQLStatsWindowSize && slowSQLWindow[index] {
		slowSQLWindowSlow--
	}
	if slowSQLWindowSamples < slowSQLStatsWindowSize {
		slowSQLWindowSamples++
	}
	slowSQLWindow[index] = isSlow
	if isSlow {
		slowSQLWindowSlow++
	}
	slowSQLWindowCursor++
}

func SnapshotSlowSQLStats() SlowSQLStatsSnapshot {
	slowSQLWindowMu.Lock()
	total := slowSQLWindowSamples
	slow := slowSQLWindowSlow
	slowSQLWindowMu.Unlock()

	slowRate := 0.0
	normalRate := 100.0
	if total > 0 {
		slowRate = float64(slow) * 100 / float64(total)
		normalRate = float64(total-slow) * 100 / float64(total)
	}

	return SlowSQLStatsSnapshot{
		TotalQueries: total,
		SlowQueries:  slow,
		NormalRate:   normalRate,
		SlowRate:     slowRate,
		ThresholdMs:  gormSlowSQLThreshold.Milliseconds(),
		WindowSize:   slowSQLStatsWindowSize,
	}
}
