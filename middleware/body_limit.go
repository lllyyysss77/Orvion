package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
)

const (
	// DefaultMaxRequestBodyBytes 默认 32 MiB:既能覆盖多图 base64 请求,又能挡住 GB 级滥用。
	DefaultMaxRequestBodyBytes int64 = 32 << 20
	envMaxRequestBodyBytes           = "ORVION_MAX_REQUEST_BODY_BYTES"
)

var (
	maxRequestBodyBytesOnce sync.Once
	maxRequestBodyBytes     int64
)

// ResolveMaxRequestBodyBytes 解析环境变量;<=0 表示不限制。首次解析结果被缓存。
func ResolveMaxRequestBodyBytes() int64 {
	maxRequestBodyBytesOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv(envMaxRequestBodyBytes))
		if raw == "" {
			maxRequestBodyBytes = DefaultMaxRequestBodyBytes
			return
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			slog.Warn("Invalid "+envMaxRequestBodyBytes+", using default", "value", raw, "default", DefaultMaxRequestBodyBytes)
			maxRequestBodyBytes = DefaultMaxRequestBodyBytes
			return
		}
		maxRequestBodyBytes = n
	})
	return maxRequestBodyBytes
}

// LimitRequestBody 用 http.MaxBytesReader 限制请求体大小,超过时返回 413。
// <=0 表示不限制。
func LimitRequestBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes <= 0 || c.Request.Body == nil {
			c.Next()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		// 预检 Content-Length(可选优化:头里就超了不用读)。
		if c.Request.ContentLength > maxBytes {
			writeTooLarge(c, maxBytes)
			return
		}
		c.Next()
		// 处理期间如 handler 读取时触发 MaxBytesError,会被 handler 自己 BadRequest;
		// 这里额外兜底:如 handler 没处理、写出的错误里含 "http: request body too large",
		// gin 会照常返回 500。为了统一体验,handler 读取失败后应调用 common.BadRequest。
	}
}

func writeTooLarge(c *gin.Context, maxBytes int64) {
	common.ErrorWithHttpStatus(c, http.StatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge,
		"request body exceeds "+strconv.FormatInt(maxBytes, 10)+" bytes")
	c.Abort()
}

// IsRequestBodyTooLarge 识别 handler 读取时因 MaxBytesReader 触发的错误。
func IsRequestBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true
	}
	// 标准库在部分场景只抛出普通 error,判一下关键字。
	return strings.Contains(err.Error(), "request body too large")
}
