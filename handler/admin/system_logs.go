package admin

import (
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg/logutil"
)

const (
	defaultSystemLogLineLimit = 200
	maxSystemLogLineLimit     = 1000
	systemLogReadWindowBytes  = 256 * 1024
	// 进程资源采样最小间隔：避免高频轮询时重复触发昂贵采样。
	processStatsSampleInterval = time.Second
)

type SystemLogSnapshot struct {
	Path      string       `json:"path"`
	Exists    bool         `json:"exists"`
	Size      int64        `json:"size"`
	UpdatedAt string       `json:"updated_at,omitempty"`
	Content   string       `json:"content"`
	Lines     int          `json:"lines"`
	Process   processStats `json:"process"`
	SlowSQL   slowSQLStats `json:"slow_sql"`
}

type processStats struct {
	MemoryBytes uint64  `json:"memory_bytes"`
	CPUPercent  float64 `json:"cpu_percent"`
	Goroutines  int     `json:"goroutines"`
	GCCount     uint32  `json:"gc_count"`
}

type slowSQLStats struct {
	TotalQueries uint64  `json:"total_queries"`
	SlowQueries  uint64  `json:"slow_queries"`
	NormalRate   float64 `json:"normal_rate"`
	SlowRate     float64 `json:"slow_rate"`
	ThresholdMs  int64   `json:"threshold_ms"`
	WindowSize   uint64  `json:"window_size"`
}

var (
	processCPUSampleMu sync.Mutex
	lastCPUTimeSeconds float64
	lastCPUSampleAt    time.Time
	lastProcessStats   processStats
)

func GetSystemLogs(c *gin.Context) {
	limit := parseSystemLogLimit(c.Query("limit"))
	path := logutil.ResolveSystemLogFilePath()

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			stats := collectProcessStats()
			common.Success(c, SystemLogSnapshot{
				Path:    path,
				Exists:  false,
				Content: "",
				Lines:   0,
				Process: stats,
				SlowSQL: collectSlowSQLStats(),
			})
			return
		}
		common.InternalServerError(c, "读取系统日志失败: "+err.Error())
		return
	}

	content, lines, err := readSystemLogTail(path, limit)
	if err != nil {
		common.InternalServerError(c, "读取系统日志失败: "+err.Error())
		return
	}

	stats := collectProcessStats()
	common.Success(c, SystemLogSnapshot{
		Path:      path,
		Exists:    true,
		Size:      info.Size(),
		UpdatedAt: info.ModTime().Format(time.RFC3339),
		Content:   content,
		Lines:     lines,
		Process:   stats,
		SlowSQL:   collectSlowSQLStats(),
	})
}

func ClearSystemLogs(c *gin.Context) {
	path := logutil.ResolveSystemLogFilePath()
	if err := logutil.ClearSystemLogFile(); err != nil {
		common.InternalServerError(c, "清空系统日志失败: "+err.Error())
		return
	}

	common.Success(c, gin.H{
		"path": path,
	})
}

func parseSystemLogLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultSystemLogLineLimit
	}
	if value > maxSystemLogLineLimit {
		return maxSystemLogLineLimit
	}
	return value
}

func readSystemLogTail(path string, lineLimit int) (string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}

	start := info.Size() - systemLogReadWindowBytes
	if start < 0 {
		start = 0
	}

	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", 0, err
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", 0, err
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if start > 0 {
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		}
	}

	text = strings.TrimRight(text, "\n")
	if text == "" {
		return "", 0, nil
	}

	lines := strings.Split(text, "\n")
	if len(lines) > lineLimit {
		lines = lines[len(lines)-lineLimit:]
	}

	return strings.Join(lines, "\n"), len(lines), nil
}

func collectProcessStats() processStats {
	stats := processStats{}

	now := time.Now()
	processCPUSampleMu.Lock()
	if !lastCPUSampleAt.IsZero() && now.Sub(lastCPUSampleAt) < processStatsSampleInterval {
		stats.MemoryBytes = lastProcessStats.MemoryBytes
		stats.CPUPercent = lastProcessStats.CPUPercent
		stats.Goroutines = lastProcessStats.Goroutines
		stats.GCCount = lastProcessStats.GCCount
		processCPUSampleMu.Unlock()
		return stats
	}
	processCPUSampleMu.Unlock()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	stats.MemoryBytes = mem.Sys
	stats.GCCount = mem.NumGC
	stats.Goroutines = runtime.NumGoroutine()

	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		processCPUSampleMu.Lock()
		lastProcessStats = stats
		lastCPUSampleAt = now
		processCPUSampleMu.Unlock()
		return stats
	}
	cpuSeconds := float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1_000_000 +
		float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1_000_000

	processCPUSampleMu.Lock()
	defer processCPUSampleMu.Unlock()

	if !lastCPUSampleAt.IsZero() && now.After(lastCPUSampleAt) {
		deltaCPU := cpuSeconds - lastCPUTimeSeconds
		deltaWall := now.Sub(lastCPUSampleAt).Seconds()
		if deltaCPU >= 0 && deltaWall > 0 {
			// 在容器环境下，GOMAXPROCS 更贴近 cgroup CPU 配额；NumCPU 可能是宿主机核数。
			cores := float64(runtime.GOMAXPROCS(0))
			if cores > 0 {
				value := (deltaCPU / deltaWall) * 100 / cores
				if value < 0 {
					value = 0
				}
				if value > 100 {
					value = 100
				}
				stats.CPUPercent = value
			}
		}
	}

	lastCPUTimeSeconds = cpuSeconds
	lastCPUSampleAt = now
	lastProcessStats = stats
	return stats
}

func collectSlowSQLStats() slowSQLStats {
	snapshot := models.SnapshotSlowSQLStats()
	return slowSQLStats{
		TotalQueries: snapshot.TotalQueries,
		SlowQueries:  snapshot.SlowQueries,
		NormalRate:   snapshot.NormalRate,
		SlowRate:     snapshot.SlowRate,
		ThresholdMs:  snapshot.ThresholdMs,
		WindowSize:   snapshot.WindowSize,
	}
}
