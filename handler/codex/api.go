package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	corehandler "github.com/racio/orvion/handler"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
	"github.com/racio/orvion/service/subscription"
)

type codexResponsesRequest struct {
	Model  string `json:"model"`
	Stream *bool  `json:"stream"`
}

const (
	codexOfficialProviderName = "codex官方"
	maxCodexErrorLogBytes     = 8 * 1024
)

// CodexAPIResponsesHandler 直接转发到 Codex 官方 /responses。
// 接口: POST /codex/api/responses
func CodexAPIResponsesHandler(c *gin.Context) {
	proxyCodexResponses(c, false)
}

// CodexAPIResponsesCompactHandler 直接转发到 Codex 官方 /responses/compact。
// 接口: POST /codex/api/responses/compact
func CodexAPIResponsesCompactHandler(c *gin.Context) {
	proxyCodexResponses(c, true)
}

func proxyCodexResponses(c *gin.Context, compact bool) {
	startReq := time.Now()

	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		common.BadRequest(c, "读取请求体失败")
		return
	}
	defer c.Request.Body.Close()

	if len(bytes.TrimSpace(rawBody)) == 0 {
		common.BadRequest(c, "请求体不能为空")
		return
	}

	var payload codexResponsesRequest
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		common.BadRequest(c, "请求体必须是合法 JSON")
		return
	}

	modelName := strings.TrimSpace(payload.Model)
	if modelName == "" {
		common.BadRequest(c, "model 不能为空")
		return
	}

	before, err := service.BeforerOpenAIRes(rawBody)
	if err != nil {
		common.BadRequest(c, "请求体解析失败: "+err.Error())
		return
	}

	valid, err := corehandler.ValidateAuthKey(c.Request.Context(), modelName)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if !valid {
		common.ErrorWithHttpStatus(c, http.StatusForbidden, http.StatusForbidden, "auth key has no permission to use this model")
		return
	}

	stream := payload.Stream != nil && *payload.Stream
	if compact && stream {
		common.BadRequest(c, "responses/compact 不支持 stream=true")
		return
	}

	subscriptionID := strings.TrimSpace(c.GetHeader("X-Codex-Subscription-Id"))
	if subscriptionID == "" {
		subscriptionID = strings.TrimSpace(c.Query("subscription_id"))
	}

	credential, err := subscription.GetCodexSubscriptionManager().ResolveRequestCredential(c.Request.Context(), subscriptionID)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrCodexSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "解析 Codex 订阅失败: "+err.Error())
		}
		return
	}

	upstreamPath := "/responses"
	if compact {
		upstreamPath = "/responses/compact"
		stream = false
	}
	upstreamURL := subscription.GetCodexBackendBaseURL() + upstreamPath
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamURL, bytes.NewReader(rawBody))
	if err != nil {
		common.InternalServerError(c, "创建上游请求失败: "+err.Error())
		return
	}
	applyCodexProxyHeaders(req.Header, c.Request.Header, credential, stream)

	resp, err := codexResponsesHTTPClient(stream).Do(req)
	if err != nil {
		common.InternalServerError(c, "调用 Codex 上游失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	logStyle := consts.StyleOpenAIRes
	baseLog := buildCodexProxyBaseLog(c, modelName, logStyle, startReq)

	if resp.StatusCode != http.StatusOK {
		responseBody, readErr := io.ReadAll(resp.Body)
		if err := saveCodexProxyErrorLog(c.Request.Context(), baseLog, resp.StatusCode, responseBody, readErr); err != nil {
			slog.Warn("保存 Codex 官方错误日志失败", "error", err, "subscription_id", credential.SubscriptionID, "compact", compact)
		}
		copyResponseHeaders(c.Writer.Header(), resp.Header)
		c.Header("X-Codex-Subscription-Id", credential.SubscriptionID)
		c.Status(resp.StatusCode)
		c.Writer.Flush()
		if len(responseBody) > 0 {
			if _, writeErr := c.Writer.Write(responseBody); writeErr != nil {
				slog.Warn("写入 Codex 官方错误响应失败", "error", writeErr, "subscription_id", credential.SubscriptionID, "compact", compact)
			}
		}
		return
	}

	logID, err := service.SaveChatLog(c.Request.Context(), baseLog)
	if err != nil {
		common.InternalServerError(c, "保存 Codex 官方日志失败: "+err.Error())
		return
	}

	pr, pw := io.Pipe()
	tee := io.TeeReader(resp.Body, pw)
	go service.RecordLog(context.Background(), startReq, pr, service.ProcesserOpenAiRes, logID, baseLog.AuthKeyID, *before, false, logStyle)

	copyResponseHeaders(c.Writer.Header(), resp.Header)
	c.Header("X-Codex-Subscription-Id", credential.SubscriptionID)
	if stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}

	c.Status(resp.StatusCode)
	c.Writer.Flush()

	if _, err := io.Copy(c.Writer, tee); err != nil {
		_ = pw.CloseWithError(err)
		slog.Warn("转发 Codex responses 响应失败", "error", err, "subscription_id", credential.SubscriptionID, "compact", compact)
		return
	}

	_ = pw.Close()
}

func codexResponsesHTTPClient(stream bool) *http.Client {
	if stream {
		return &http.Client{}
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func applyCodexProxyHeaders(target http.Header, incoming http.Header, credential *subscription.CodexRequestCredential, stream bool) {
	target.Set("Content-Type", "application/json")
	target.Set("Authorization", "Bearer "+credential.AccessToken)
	target.Set("Version", firstHeaderOrDefault(incoming, "Version", subscription.GetCodexClientVersion()))
	target.Set("Openai-Beta", firstHeaderOrDefault(incoming, "Openai-Beta", "responses=experimental"))
	target.Set("Session_id", firstHeaderOrDefault(incoming, "Session_id", uuid.NewString()))
	target.Set("User-Agent", firstHeaderOrDefault(incoming, "User-Agent", subscription.GetCodexClientUserAgent()))
	if stream {
		target.Set("Accept", "text/event-stream")
	} else {
		target.Set("Accept", "application/json")
	}
	target.Set("Connection", "Keep-Alive")
	target.Set("Originator", firstHeaderOrDefault(incoming, "Originator", subscription.GetCodexClientOriginator()))
	if credential.AccountID != "" {
		target.Set("Chatgpt-Account-Id", credential.AccountID)
	}
}

func copyResponseHeaders(target http.Header, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func firstHeaderOrDefault(header http.Header, key, fallback string) string {
	if header != nil {
		if value := strings.TrimSpace(header.Get(key)); value != "" {
			return value
		}
	}
	return fallback
}

func buildCodexProxyBaseLog(c *gin.Context, modelName, style string, startReq time.Time) models.ChatLog {
	authKeyID, _ := c.Request.Context().Value(consts.ContextKeyAuthKeyID).(uint)
	return models.ChatLog{
		Name:          modelName,
		ProviderModel: modelName,
		ProviderName:  codexOfficialProviderName,
		Status:        "success",
		Style:         style,
		UserAgent:     c.Request.UserAgent(),
		RemoteIP:      c.ClientIP(),
		AuthKeyID:     authKeyID,
		ChatIO:        0,
		Retry:         0,
		ProxyTimeMs:   int(time.Since(startReq).Milliseconds()),
	}
}

func saveCodexProxyErrorLog(ctx context.Context, baseLog models.ChatLog, status int, body []byte, readErr error) error {
	message := fmt.Sprintf("status: %d", status)
	errorBody := formatCodexErrorBodyForLog(body)
	if errorBody != "" {
		message = fmt.Sprintf("%s, body: %s", message, errorBody)
	}
	if readErr != nil {
		message = fmt.Sprintf("%s, read_error: %v", message, readErr)
	}

	baseLog = baseLog.WithError(errors.New(message))
	baseLog.Size = len(body)
	_, err := service.SaveChatLog(ctx, baseLog)
	return err
}

func formatCodexErrorBodyForLog(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	truncated := false
	totalBytes := len(body)
	safe := body
	if len(safe) > maxCodexErrorLogBytes {
		safe = safe[:maxCodexErrorLogBytes]
		truncated = true
	}

	if utf8.Valid(safe) {
		text := string(safe)
		if truncated {
			return fmt.Sprintf("%s...(已截断，总计 %d 字节)", text, totalBytes)
		}
		return text
	}

	b64 := base64.StdEncoding.EncodeToString(safe)
	if truncated {
		return fmt.Sprintf("base64:%s...(已截断，总计 %d 字节)", b64, totalBytes)
	}
	return "base64:" + b64
}
