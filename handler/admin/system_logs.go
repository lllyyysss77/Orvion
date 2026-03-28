package admin

import (
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/pkg/logutil"
)

const (
	defaultSystemLogLineLimit = 200
	maxSystemLogLineLimit     = 1000
	systemLogReadWindowBytes  = 256 * 1024
)

type SystemLogSnapshot struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Content   string `json:"content"`
	Lines     int    `json:"lines"`
}

func GetSystemLogs(c *gin.Context) {
	limit := parseSystemLogLimit(c.Query("limit"))
	path := logutil.ResolveSystemLogFilePath()

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			common.Success(c, SystemLogSnapshot{
				Path:    path,
				Exists:  false,
				Content: "",
				Lines:   0,
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

	common.Success(c, SystemLogSnapshot{
		Path:      path,
		Exists:    true,
		Size:      info.Size(),
		UpdatedAt: info.ModTime().Format(time.RFC3339),
		Content:   content,
		Lines:     lines,
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
