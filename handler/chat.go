package handler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"syscall"
	"time"

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
		if statusCode, ok := service.UpstreamStatusCode(err); ok {
			common.ErrorWithHttpStatus(c, statusCode, statusCode, err.Error())
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	defer res.Body.Close()

	logRef, err := service.SaveChatLog(ctx, *log)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	spool, err := newResponseSpool()
	if err != nil {
		common.InternalServerError(c, "create response spool: "+err.Error())
		return
	}
	var streamErr error
	defer func() {
		reader, readerErr := spool.Reader(streamErr)
		if readerErr != nil {
			captureError := "response capture incomplete: " + readerErr.Error()
			if updateErr := models.UpdateMonthlyChatLogByRef(context.Background(), logRef, map[string]any{"error": captureError}); updateErr != nil {
				slog.Error("标记响应日志不完整失败", "log_id", logRef.ID, "log_uuid", logRef.UUID, "error", updateErr)
			}
			slog.Error("响应日志暂存失败，Usage 与 IO 日志不完整", "log_id", logRef.ID, "log_uuid", logRef.UUID, "error", readerErr)
			return
		}
		pkg.GoSafe("handler.record_log", func() {
			service.RecordLog(context.Background(), startReq, log.FirstChunkTimeMs, reader, postProcessor, logRef, log.AuthKeyID, effectiveBefore, effectiveProvidersWithMeta.IOLog, logStyle)
		})
	}()

	clientWriter := &countingWriter{writer: c.Writer}
	mirror := io.MultiWriter(spool, clientWriter)
	if logStyle == consts.StyleOpenAI {
		if before.Stream {
			writeHeader(c, true, res.Header, logStyle)
			if err := runtimesvc.CopyStreamWithTransform(res.Body, mirror, runtimesvc.NormalizeOpenAIStreamLine); err != nil {
				streamErr = err
				logStreamCopyError("stream copy", err)
				return
			}
		} else {
			body, readErr := io.ReadAll(res.Body)
			if readErr != nil {
				streamErr = readErr
				slog.Error("read response body", "err", readErr)
				return
			}
			if endpoint == "videos" {
				proxyURL := resolveVideoPollProxyURL(effectiveProvidersWithMeta, log)
				polledBody, pollErr := waitForOpenAIVideoCompletion(ctx, res.Request, body, proxyURL)
				if pollErr != nil {
					slog.Warn("视频任务轮询失败，回退当前响应",
						"error", pollErr,
						"model", log.Name,
						"provider", log.ProviderName,
					)
				}
				body = polledBody
			}
			normalized := runtimesvc.NormalizeOpenAIChatCompletionPayload(body, false)
			writeHeader(c, false, openAINonStreamHeader(res.Header), logStyle)
			if _, writeErr := mirror.Write(normalized); writeErr != nil {
				streamErr = writeErr
				slog.Error("write response body", "err", writeErr)
				return
			}
		}
		return
	}

	writeHeader(c, before.Stream, res.Header, logStyle)
	if _, err := io.Copy(mirror, res.Body); err != nil {
		streamErr = err
		logStreamCopyError("io copy", err)
		return
	}
}

func openAINonStreamHeader(header http.Header) http.Header {
	next := header.Clone()
	next.Del("Content-Length")
	next.Set("Content-Type", "application/json; charset=utf-8")
	return next
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

type countingWriter struct {
	writer  io.Writer
	written int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
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
