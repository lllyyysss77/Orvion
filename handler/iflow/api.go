package iflow

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
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	corehandler "github.com/racio/orvion/handler"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/racio/orvion/service/subscription"
)

const (
	iflowOfficialProviderName = "iflow官方"
	iflowClientUserAgent      = "iFlow-Cli"
	maxIFlowErrorLogBytes     = 8 * 1024
)

type iflowChatCompletionsRequest struct {
	Model string `json:"model"`
}

// IFlowAPIChatCompletionsHandler 直接转发到 iFlow 官方 /chat/completions。
// 接口: POST /iflow/v1/chat/completions
func IFlowAPIChatCompletionsHandler(c *gin.Context) {
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

	var payload iflowChatCompletionsRequest
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		common.BadRequest(c, "请求体必须是合法 JSON")
		return
	}
	modelName := strings.TrimSpace(payload.Model)
	if modelName == "" {
		common.BadRequest(c, "model 不能为空")
		return
	}

	before, err := service.BeforerOpenAI(rawBody)
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

	subscriptionID := strings.TrimSpace(c.GetHeader("X-IFlow-Subscription-Id"))
	if subscriptionID == "" {
		subscriptionID = strings.TrimSpace(c.Query("subscription_id"))
	}
	credential, err := subscription.GetIFlowSubscriptionManager().ResolveRequestCredential(c.Request.Context(), subscriptionID)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrIFlowSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "解析 iFlow 订阅失败: "+err.Error())
		}
		return
	}

	upstreamURL := subscription.GetIFlowAPIBaseURL() + "/chat/completions"
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamURL, bytes.NewReader(rawBody))
	if err != nil {
		common.InternalServerError(c, "创建上游请求失败: "+err.Error())
		return
	}
	applyIFlowProxyHeaders(req.Header, c.Request.Header, credential, before.Stream)

	resp, err := iflowChatHTTPClient(before.Stream).Do(req)
	if err != nil {
		common.InternalServerError(c, "调用 iFlow 上游失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	logStyle := consts.StyleOpenAI
	baseLog := buildIFlowProxyBaseLog(c, modelName, logStyle, startReq)

	if resp.StatusCode != http.StatusOK {
		responseBody, readErr := io.ReadAll(resp.Body)
		if err := saveIFlowProxyErrorLog(c.Request.Context(), baseLog, resp.StatusCode, responseBody, readErr); err != nil {
			slog.Warn("保存 iFlow 官方错误日志失败", "error", err, "subscription_id", credential.SubscriptionID)
		}
		copyResponseHeaders(c.Writer.Header(), resp.Header)
		c.Header("X-IFlow-Subscription-Id", credential.SubscriptionID)
		c.Status(resp.StatusCode)
		c.Writer.Flush()
		if len(responseBody) > 0 {
			if _, writeErr := c.Writer.Write(responseBody); writeErr != nil {
				slog.Warn("写入 iFlow 官方错误响应失败", "error", writeErr, "subscription_id", credential.SubscriptionID)
			}
		}
		return
	}

	logID, err := service.SaveChatLog(c.Request.Context(), baseLog)
	if err != nil {
		common.InternalServerError(c, "保存 iFlow 官方日志失败: "+err.Error())
		return
	}

	pr, pw := io.Pipe()
	go service.RecordLog(context.Background(), startReq, pr, service.ProcesserOpenAI, logID, baseLog.AuthKeyID, *before, false, logStyle)

	copyResponseHeaders(c.Writer.Header(), resp.Header)
	c.Header("X-IFlow-Subscription-Id", credential.SubscriptionID)
	if before.Stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}

	c.Status(resp.StatusCode)
	c.Writer.Flush()

	mirror := io.MultiWriter(c.Writer, pw)
	if before.Stream {
		if err := runtimesvc.CopyStreamWithTransform(resp.Body, mirror, runtimesvc.NormalizeOpenAIStreamLine); err != nil {
			_ = pw.CloseWithError(err)
			slog.Warn("转发 iFlow chat/completions 响应失败", "error", err, "subscription_id", credential.SubscriptionID)
			return
		}
	} else {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			_ = pw.CloseWithError(err)
			slog.Warn("读取 iFlow chat/completions 响应失败", "error", err, "subscription_id", credential.SubscriptionID)
			return
		}
		normalized := runtimesvc.NormalizeOpenAIChatCompletionPayload(body, false)
		if _, err := mirror.Write(normalized); err != nil {
			_ = pw.CloseWithError(err)
			slog.Warn("写入 iFlow chat/completions 响应失败", "error", err, "subscription_id", credential.SubscriptionID)
			return
		}
	}
	_ = pw.Close()
}

func iflowChatHTTPClient(stream bool) *http.Client {
	if stream {
		return &http.Client{}
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func applyIFlowProxyHeaders(target http.Header, incoming http.Header, credential *subscription.IFlowRequestCredential, stream bool) {
	target.Set("Content-Type", "application/json")
	target.Set("Authorization", "Bearer "+credential.APIKey)
	target.Set("User-Agent", firstHeaderOrDefault(incoming, "User-Agent", iflowClientUserAgent))
	if stream {
		target.Set("Accept", "text/event-stream")
	} else {
		target.Set("Accept", "application/json")
	}
	target.Set("Connection", "Keep-Alive")
}

func buildIFlowProxyBaseLog(c *gin.Context, modelName, style string, startReq time.Time) models.ChatLog {
	authKeyID, _ := c.Request.Context().Value(consts.ContextKeyAuthKeyID).(uint)
	return models.ChatLog{
		Name:          modelName,
		ProviderModel: modelName,
		ProviderName:  iflowOfficialProviderName,
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

func saveIFlowProxyErrorLog(ctx context.Context, baseLog models.ChatLog, status int, body []byte, readErr error) error {
	message := fmt.Sprintf("status: %d", status)
	errorBody := formatIFlowErrorBodyForLog(body)
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

func formatIFlowErrorBodyForLog(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	truncated := false
	totalBytes := len(body)
	safe := body
	if len(safe) > maxIFlowErrorLogBytes {
		safe = safe[:maxIFlowErrorLogBytes]
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
