package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/middleware"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/service"
	runtimesvc "github.com/racio/orvion/service/runtime"
)

func ChatCompletionsHandler(c *gin.Context) {
	chatHandler(c, service.BeforerOpenAI, service.ProcesserOpenAI, consts.StyleOpenAI, consts.StyleOpenAI, "chat")
}

func ResponsesHandler(c *gin.Context) {
	chatHandler(c, service.BeforerOpenAIRes, service.ProcesserOpenAiRes, consts.StyleOpenAIRes, consts.StyleOpenAIRes, "responses")
}

// ResponsesCompactHandler 转发 OpenAI 兼容 responses/compact 接口:
// POST /v1/responses/compact
func ResponsesCompactHandler(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyOpenAIEndpoint, "responses/compact")
	c.Request = c.Request.WithContext(ctx)
	// 上下文压缩接口沿用 responses 能力校验规则
	chatHandler(c, service.BeforerOpenAIRes, service.ProcesserOpenAiRes, consts.StyleOpenAIRes, consts.StyleOpenAIRes, "responses")
}

func Messages(c *gin.Context) {
	chatHandler(c, service.BeforerAnthropic, service.ProcesserAnthropic, consts.StyleAnthropic, consts.StyleAnthropic, "messages")
}

// EmbeddingsHandler 转发 OpenAI 兼容 embeddings 接口:
// POST /v1/embeddings
func EmbeddingsHandler(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyOpenAIEndpoint, "embeddings")
	c.Request = c.Request.WithContext(ctx)
	chatHandler(c, service.BeforerOpenAI, service.ProcesserOpenAI, consts.StyleOpenAI, consts.StyleOpenAIEmbeddings, "embeddings")
}

// RerankHandler 转发 OpenAI 兼容 rerank 接口:
// POST /v1/rerank
func RerankHandler(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyOpenAIEndpoint, "rerank")
	c.Request = c.Request.WithContext(ctx)
	chatHandler(c, service.BeforerOpenAI, service.ProcesserOpenAI, consts.StyleOpenAI, consts.StyleOpenAI, "rerank")
}

// ImagesGenerationsHandler 转发 OpenAI 兼容 images/generations 接口:
// POST /v1/images/generations
func ImagesGenerationsHandler(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyOpenAIEndpoint, "images/generations")
	c.Request = c.Request.WithContext(ctx)
	chatHandler(c, service.BeforerOpenAIMedia, service.ProcesserOpenAI, consts.StyleOpenAI, consts.StyleOpenAI, "images")
}

// ImagesEditsHandler 转发 OpenAI 兼容 images/edits 接口:
// POST /v1/images/edits
func ImagesEditsHandler(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyOpenAIEndpoint, "images/edits")
	c.Request = c.Request.WithContext(ctx)
	chatHandler(c, service.BeforerOpenAIMedia, service.ProcesserOpenAI, consts.StyleOpenAI, consts.StyleOpenAI, "images")
}

// VideosHandler 转发 OpenAI 兼容 videos 接口:
// POST /v1/videos
func VideosHandler(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), consts.ContextKeyOpenAIEndpoint, "videos")
	c.Request = c.Request.WithContext(ctx)
	chatHandler(c, service.BeforerOpenAIMedia, service.ProcesserOpenAI, consts.StyleOpenAI, consts.StyleOpenAI, "videos")
}

func resolveRequestPath(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if path := strings.TrimSpace(c.FullPath()); path != "" {
		return path
	}
	return strings.TrimSpace(c.Request.URL.Path)
}

func chatHandler(c *gin.Context, preProcessor service.Beforer, postProcessor service.Processer, _ string, logStyle string, endpoint string) {
	// 读取原始请求体
	reqBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if middleware.IsRequestBodyTooLarge(err) {
			common.ErrorWithHttpStatus(c, http.StatusRequestEntityTooLarge, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	c.Request.Body.Close()
	// 预处理、提取模型参数
	before, err := preProcessor(reqBody)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	shouldLogDialogueIO := !isDialogueEndpoint(endpoint)
	if shouldLogDialogueIO {
		slog.Info("接收到客户端消息",
			"path", c.FullPath(),
			"endpoint", endpoint,
			"model", before.Model,
			"stream", before.Stream,
			"payload_bytes", len(reqBody),
			"payload_preview", formatBodyPreview(reqBody),
		)
	}

	ctx := c.Request.Context()
	requestPath := resolveRequestPath(c)
	if endpoint != "" {
		if err := service.ValidateModelCapability(ctx, before.Model, endpoint); err != nil {
			common.BadRequest(c, err.Error())
			return
		}
	}
	// 校验 authKey 是否有权限使用该模型
	valid, err := ValidateAuthKey(ctx, before.Model)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if !valid {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, "auth key has no permission to use this model")
		return
	}
	// 按模型获取可用 provider
	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, logStyle, endpoint, requestPath, *before)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	startReq := time.Now()
	// 调用负载均衡后的 provider 并转发
	res, log, effectiveBefore, effectiveProvidersWithMeta, err := service.BalanceChatWithLimiter(c, startReq, logStyle, requestPath, *before, providersWithMeta, models.ReqMeta{
		Header:    c.Request.Header,
		RemoteIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	})
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	defer res.Body.Close()

	logRef, err := service.SaveChatLog(ctx, *log)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	pr, pipeWriter := io.Pipe()
	// 用 asyncMirrorWriter 把 pw 写入解耦:RecordLog 消费慢或 io.Pipe 32KiB
	// 缓冲吃满时,Write 不阻塞,保证上游响应读链不被拖死。
	pw := newAsyncMirrorWriter(pipeWriter, 256)
	// 异步处理输出并记录 tokens
	pkg.GoSafe("handler.record_log", func() {
		service.RecordLog(context.Background(), startReq, log.FirstChunkTimeMs, pr, postProcessor, logRef, log.AuthKeyID, effectiveBefore, effectiveProvidersWithMeta.IOLog, logStyle)
	})

	writeHeader(c, before.Stream, res.Header, logStyle)
	clientWriter := &countingWriter{writer: c.Writer}
	mirror := io.MultiWriter(clientWriter, pw)
	if logStyle == consts.StyleOpenAI {
		if before.Stream {
			if err := runtimesvc.CopyStreamWithTransform(res.Body, mirror, runtimesvc.NormalizeOpenAIStreamLine); err != nil {
				_ = pw.CloseWithError(err)
				logStreamCopyError("stream copy", err)
				return
			}
			if shouldLogDialogueIO {
				slog.Info("已发送响应消息",
					"path", c.FullPath(),
					"endpoint", endpoint,
					"model", before.Model,
					"stream", true,
					"response_bytes", clientWriter.written,
					"mirror_dropped_bytes", pw.Dropped(),
				)
			}
		} else {
			body, readErr := io.ReadAll(res.Body)
			if readErr != nil {
				_ = pw.CloseWithError(readErr)
				slog.Error("read response body", "err", readErr)
				return
			}
			normalized := runtimesvc.NormalizeOpenAIChatCompletionPayload(body, false)
			if _, writeErr := mirror.Write(normalized); writeErr != nil {
				_ = pw.CloseWithError(writeErr)
				slog.Error("write response body", "err", writeErr)
				return
			}
			if shouldLogDialogueIO {
				slog.Info("已发送响应消息",
					"path", c.FullPath(),
					"endpoint", endpoint,
					"model", before.Model,
					"stream", false,
					"response_bytes", clientWriter.written,
					"response_preview", formatBodyPreview(normalized),
					"mirror_dropped_bytes", pw.Dropped(),
				)
			}
		}
		_ = pw.Close()
		return
	}

	tee := io.TeeReader(res.Body, pw)
	if _, err := io.Copy(clientWriter, tee); err != nil {
		_ = pw.CloseWithError(err)
		logStreamCopyError("io copy", err)
		return
	}
	if shouldLogDialogueIO {
		slog.Info("已发送响应消息",
			"path", c.FullPath(),
			"endpoint", endpoint,
			"model", before.Model,
			"stream", before.Stream,
			"response_bytes", clientWriter.written,
			"mirror_dropped_bytes", pw.Dropped(),
		)
	}

	_ = pw.Close()
}

func writeHeader(c *gin.Context, stream bool, header http.Header, logStyle string) {
	for k, values := range header {
		for _, value := range values {
			c.Writer.Header().Add(k, value)
		}
	}

	if stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	} else if logStyle == consts.StyleOpenAIEmbeddings || logStyle == consts.StyleGeminiEmbeddings {
		// 兼容部分上游返回 text/plain，避免客户端按字符串处理导致解析失败。
		c.Header("Content-Type", "application/json; charset=utf-8")
	}
	c.Writer.Flush()
}

func formatHeadersJSON(header http.Header) string {
	content, err := json.MarshalIndent(header, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(content)
}

func logStreamCopyError(msg string, err error) {
	if err == nil {
		return
	}
	if isExpectedClientDisconnect(err) {
		slog.Debug(msg, "err", err)
		return
	}
	slog.Error(msg, "err", err)
}

func isExpectedClientDisconnect(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "closed pipe") ||
		strings.Contains(errText, "broken pipe") ||
		strings.Contains(errText, "client disconnected")
}

func isDialogueEndpoint(endpoint string) bool {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "chat", "responses", "messages":
		return true
	default:
		return false
	}
}

type countingWriter struct {
	writer  io.Writer
	written int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
}

func formatBodyPreview(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	const maxPreviewBytes = 512
	preview := raw
	truncated := false
	if len(preview) > maxPreviewBytes {
		preview = preview[:maxPreviewBytes]
		truncated = true
	}
	if !utf8.Valid(preview) {
		return fmt.Sprintf("[非 UTF-8 内容，总长度 %d 字节]", len(raw))
	}
	text := strings.ReplaceAll(string(preview), "\n", "\\n")
	text = strings.ReplaceAll(text, "\r", "\\r")
	if truncated {
		return text + "...(已截断)"
	}
	return text
}

// 校验auhtKey的模型使用权限
func ValidateAuthKey(ctx context.Context, model string) (bool, error) {
	// 验证是否为允许全部模型
	allowAll, ok := ctx.Value(consts.ContextKeyAllowAllModel).(bool)
	if !ok {
		return false, errors.New("invalid auth key")
	}
	if allowAll {
		return true, nil
	}
	// 验证是否有权限使用该模型
	allowedModels, ok := ctx.Value(consts.ContextKeyAllowModels).([]string)
	if !ok {
		return false, errors.New("invalid auth key")
	}
	return slices.Contains(allowedModels, model), nil
}
