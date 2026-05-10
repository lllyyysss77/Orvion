package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/atopos31/nsxno/react"
	"github.com/gin-gonic/gin"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

const (
	testOpenAI = `{
        "model": "gpt-4.1",
        "messages": [
            {
                "role": "user",
                "content": "Please reply me yes or no"
            }
        ]
    }`

	testOpenAIRes = `{
		"model": "gpt-4.1",
		"input": [
			{
				"role": "user",
				"content": [
					{
						"type": "input_text",
						"text": "Please reply me yes or no"
					}
				]
			}
		]
  	}`

	testAnthropic = `{
    	"model": "claude-sonnet-4-5",
		"system": [
			{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
				"cache_control": {
					"type": "ephemeral"
				}
			}
		],
    	"messages": [
      		{
        		"role": "user", 
        		"content": [
					{
						"type": "text",
						"text": "Please reply me yes or no",
						"cache_control": {
							"type": "ephemeral"
						}
					}
				]
      		}
    	],
		"tools": [],
		"metadata": {
			"user_id": "user_a1b2c3d4e5f6789012345678901234567890abcdef1234567890abcdef123456_account__session_12345678-90ab-cdef-1234-567890abcdef"
		},
		"max_tokens": 32000,
		"thinking": {
			"budget_tokens": 31999,
			"type": "enabled"
		},
		"stream": true
 	}`

	testGemini = `{
		"contents": [
			{
				"parts": [
					{
						"text": "Please reply me yes or no"
					}
				]
			}
		]
	}`
)

func ProviderTestHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		common.BadRequest(c, "Invalid ID format")
		return
	}
	ctx := c.Request.Context()

	chatModel, err := FindChatModel(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	// Create the provider instance
	resolvedStyle := providers.ResolveStyle("", chatModel.Config)
	providerInstance, err := providers.NewWithProxy(chatModel.Config, chatModel.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
		return
	}

	// Test connectivity by fetching models
	responseHeaderTimeout := time.Second * time.Duration(30)
	var testBody []byte
	switch resolvedStyle {
	case consts.StyleOpenAI:
		testBody = []byte(testOpenAI)
	case consts.StyleAnthropic:
		testBody = []byte(testAnthropic)
	case consts.StyleOpenAIRes:
		testBody = []byte(testOpenAIRes)
	case consts.StyleGemini:
		testBody = []byte(testGemini)
	default:
		common.BadRequest(c, "Invalid provider type")
		return
	}
	withHeader := false
	if chatModel.WithHeader != nil {
		withHeader = *chatModel.WithHeader
	}
	header := service.BuildHeaders(c.Request.Header, withHeader, chatModel.CustomerHeaders, false)
	extraHeaders := loadDefaultHeaders()
	if header == nil {
		header = http.Header{}
	}
	mergeHeaders(header, extraHeaders, map[string]struct{}{
		"authorization":   {},
		"accept-encoding": {},
		"content-length":  {},
		"host":            {},
	})
	// 避免将浏览器的压缩协商（br/zstd）透传到上游，导致回包为不可读压缩字节。
	header.Del("Accept-Encoding")
	header.Set("Accept-Encoding", "identity")
	req, err := providerInstance.BuildReq(ctx, header, chatModel.Model, []byte(testBody))
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to connect to provider: "+err.Error())
		return
	}
	client := &http.Client{
		Timeout: responseHeaderTimeout,
	}
	res, err := client.Do(req)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to connect to provider: "+err.Error())
		return
	}
	defer res.Body.Close()

	content, err := io.ReadAll(res.Body)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Failed to send request: "+err.Error())
		return
	}
	content = decodeHTTPResponseBody(res.Header, content)

	if res.StatusCode != http.StatusOK {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, fmt.Sprintf("code: %d body: %s", res.StatusCode, string(content)))
		return
	}

	common.SuccessWithMessage(c, string(content), nil)
}

type ChatTestRequest struct {
	Prompt   string `json:"prompt"`
	Endpoint string `json:"endpoint"`
	Stream   bool   `json:"stream"`
	ImageURL string `json:"image_url"`
	MaskURL  string `json:"mask_url"`
	Size     string `json:"size"`
}

func resolveProviderStyleForEndpoint(chatModel *ChatModel, endpoint string) string {
	baseStyle := providers.ResolveStyle("", chatModel.Config)

	switch endpoint {
	case "messages":
		return consts.StyleAnthropic
	case "responses":
		return consts.StyleOpenAIRes
	case "images/generations", "images/edits", "videos":
		return consts.StyleOpenAI
	case "chat/completions":
		if baseStyle == consts.StyleGemini {
			return consts.StyleGemini
		}
		return consts.StyleOpenAI
	default:
		return baseStyle
	}
}

func ModelChatTestHandler(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	var req ChatTestRequest
	isMultipart := strings.Contains(strings.ToLower(c.ContentType()), "multipart/form-data")
	var imageFileName string
	var imageBytes []byte
	var maskFileName string
	var maskBytes []byte

	if isMultipart {
		req.Prompt = c.PostForm("prompt")
		req.Endpoint = c.PostForm("endpoint")
		req.Size = c.PostForm("size")
		streamRaw := strings.TrimSpace(strings.ToLower(c.PostForm("stream")))
		req.Stream = streamRaw == "1" || streamRaw == "true" || streamRaw == "yes" || streamRaw == "on"

		if fileHeader, err := c.FormFile("image"); err == nil && fileHeader != nil {
			file, err := fileHeader.Open()
			if err != nil {
				common.BadRequest(c, "读取图片失败: "+err.Error())
				return
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil {
				common.BadRequest(c, "读取图片失败: "+err.Error())
				return
			}
			imageFileName = fileHeader.Filename
			imageBytes = data
		}

		if fileHeader, err := c.FormFile("mask"); err == nil && fileHeader != nil {
			file, err := fileHeader.Open()
			if err != nil {
				common.BadRequest(c, "读取遮罩失败: "+err.Error())
				return
			}
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil {
				common.BadRequest(c, "读取遮罩失败: "+err.Error())
				return
			}
			maskFileName = fileHeader.Filename
			maskBytes = data
		}
	} else {
		if err := c.ShouldBindJSON(&req); err != nil {
			common.BadRequest(c, "Invalid request body: "+err.Error())
			return
		}
	}

	if strings.TrimSpace(req.Prompt) == "" {
		common.BadRequest(c, "Prompt is required")
		return
	}
	endpoint := normalizeTestEndpoint(req.Endpoint)
	if endpoint == "" {
		common.BadRequest(c, "Invalid endpoint")
		return
	}
	streamRequested := req.Stream && isStreamableTestEndpoint(endpoint)

	ctx := c.Request.Context()
	chatModel, err := FindChatModel(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	withHeader := false
	if chatModel.WithHeader != nil {
		withHeader = *chatModel.WithHeader
	}
	startReq := time.Now()
	header := service.BuildHeaders(c.Request.Header, withHeader, chatModel.CustomerHeaders, streamRequested)
	extraHeaders := loadDefaultHeaders()
	if header == nil {
		header = http.Header{}
	}
	mergeHeaders(header, extraHeaders, map[string]struct{}{
		"authorization":   {},
		"accept-encoding": {},
		"content-length":  {},
		"host":            {},
	})
	// 避免将浏览器的压缩协商（br/zstd）透传到上游，导致回包为不可读压缩字节。
	header.Del("Accept-Encoding")
	header.Set("Accept-Encoding", "identity")

	requestStyle := resolveProviderStyleForEndpoint(chatModel, endpoint)
	providerInstance, err := providers.NewForStyleWithProxy(requestStyle, chatModel.Config, chatModel.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
		return
	}

	var request *http.Request
	var requestBodyForLog []byte
	if endpoint == "images/edits" {
		if requestStyle != consts.StyleOpenAI {
			recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, nil, "images/edits 仅支持 OpenAI 兼容提供商")
			common.BadRequest(c, "images/edits 仅支持 OpenAI 兼容提供商")
			return
		}
		requestBodyForLog, err = buildImageEditLogBody(chatModel, req, imageFileName, maskFileName, len(imageBytes) > 0, len(maskBytes) > 0)
		if err != nil {
			common.InternalServerError(c, "构建测试日志请求体失败")
			return
		}
		if isMultipart {
			if len(imageBytes) == 0 {
				recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, requestBodyForLog, "images/edits 需要上传 image 文件")
				common.BadRequest(c, "images/edits 需要上传 image 文件")
				return
			}
			request, err = buildOpenAIImageEditRequestFromUpload(ctx, chatModel, header, req, imageFileName, imageBytes, maskFileName, maskBytes)
		} else {
			request, err = buildOpenAIImageEditRequest(ctx, chatModel, header, req)
		}
	} else {
		body, err := buildChatTestBody(requestStyle, endpoint, req, streamRequested, imageFileName, imageBytes)
		if err != nil {
			recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, nil, err.Error())
			common.BadRequest(c, err.Error())
			return
		}
		requestBodyForLog, err = enrichTestRequestBodyForLog(body, chatModel.Model)
		if err != nil {
			common.InternalServerError(c, "构建测试日志请求体失败")
			return
		}
		ctx = context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, endpoint)
		if requestStyle == consts.StyleGemini && streamRequested {
			ctx = context.WithValue(ctx, consts.ContextKeyGeminiStream, true)
		}
		request, err = providerInstance.BuildReq(ctx, header, chatModel.Model, body)
	}
	if err != nil {
		recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, requestBodyForLog, "Failed to build request: "+err.Error())
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to build request: "+err.Error())
		return
	}

	client := &http.Client{Timeout: time.Second * 60}
	res, err := client.Do(request)
	if err != nil {
		recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, requestBodyForLog, "Failed to send request: "+err.Error())
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to send request: "+err.Error())
		return
	}
	defer res.Body.Close()

	contentType := strings.ToLower(res.Header.Get("Content-Type"))
	if streamRequested && strings.Contains(contentType, "text/event-stream") {
		writeHeader(c, true, res.Header, requestStyle)
		var responseBuffer bytes.Buffer
		dst := io.MultiWriter(&streamFlushWriter{writer: c.Writer}, &responseBuffer)
		if _, err := io.Copy(dst, res.Body); err != nil {
			if isExpectedClientDisconnect(err) {
				slog.Debug("测试场流式请求已断开", "error", err)
				return
			}
			recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, requestBodyForLog, "Failed to stream response: "+err.Error())
			logStreamCopyError("test stream copy", err)
			return
		}
		if err := recordTestChatSuccess(ctx, c, chatModel, requestStyle, endpoint, startReq, requestBodyForLog, responseBuffer.Bytes()); err != nil {
			slog.Error("记录测试场流式请求日志失败", "error", err, "model", chatModel.ModelName, "provider", chatModel.ProviderName)
		}
		return
	}

	content, err := io.ReadAll(res.Body)
	if err != nil {
		recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, requestBodyForLog, "Failed to read response: "+err.Error())
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Failed to read response: "+err.Error())
		return
	}
	content = decodeHTTPResponseBody(res.Header, content)

	if res.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("code: %d body: %s", res.StatusCode, string(content))
		recordTestChatFailure(ctx, c, chatModel, requestStyle, startReq, requestBodyForLog, errMsg)
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, errMsg)
		return
	}

	if err := recordTestChatSuccess(ctx, c, chatModel, requestStyle, endpoint, startReq, requestBodyForLog, content); err != nil {
		slog.Error("记录测试场请求日志失败", "error", err, "model", chatModel.ModelName, "provider", chatModel.ProviderName)
	}

	text := extractChatContent(requestStyle, endpoint, content)
	if strings.TrimSpace(text) == "" {
		text = extractChatContentFromSSE(requestStyle, endpoint, content)
	}
	if strings.TrimSpace(text) == "" {
		text = string(content)
	}
	responsePayload := map[string]any{
		"content": text,
	}
	if imageURL := extractChatImagePreview(requestStyle, endpoint, content); imageURL != "" {
		responsePayload["image_url"] = imageURL
	}

	common.Success(c, responsePayload)
}

func buildChatTestBody(style string, endpoint string, req ChatTestRequest, stream bool, imageFileName string, imageBytes []byte) ([]byte, error) {
	prompt := strings.TrimSpace(req.Prompt)
	hasImageUpload := len(imageBytes) > 0
	imageMimeType := ""
	imageBase64 := ""
	imageDataURL := ""
	if hasImageUpload {
		imageMimeType = detectImageMIMEType(imageFileName, imageBytes)
		imageBase64 = base64.StdEncoding.EncodeToString(imageBytes)
		imageDataURL = fmt.Sprintf("data:%s;base64,%s", imageMimeType, imageBase64)
	}
	switch style {
	case consts.StyleOpenAI:
		switch endpoint {
		case "chat/completions":
			content := []map[string]any{
				{"type": "text", "text": prompt},
			}
			if hasImageUpload {
				content = append(content, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": imageDataURL,
					},
				})
			}
			return json.Marshal(map[string]any{
				"messages": []map[string]any{
					{"role": "user", "content": content},
				},
				"temperature": 0.2,
				"stream":      stream,
			})
		case "responses":
			content := []map[string]any{
				{"type": "input_text", "text": prompt},
			}
			if hasImageUpload {
				content = append(content, map[string]any{
					"type":      "input_image",
					"image_url": imageDataURL,
				})
			}
			return json.Marshal(map[string]any{
				"instructions": "你是一个有帮助的助手。",
				"input": []map[string]any{
					{
						"role":    "user",
						"content": content,
					},
				},
				"store":  false,
				"stream": stream,
			})
		case "images/generations":
			payload := map[string]any{
				"prompt": prompt,
			}
			if strings.TrimSpace(req.Size) != "" {
				payload["size"] = strings.TrimSpace(req.Size)
			}
			return json.Marshal(payload)
		case "images/edits":
			return nil, fmt.Errorf("images/edits 使用 multipart 方式发送")
		case "videos":
			return json.Marshal(map[string]any{
				"prompt": prompt,
			})
		case "messages":
			return nil, fmt.Errorf("OpenAI 不支持 messages 端点")
		default:
			return nil, fmt.Errorf("unsupported endpoint")
		}
	case consts.StyleOpenAIRes:
		if endpoint != "responses" {
			return nil, fmt.Errorf("该提供商仅支持 responses 端点")
		}
		content := []map[string]any{
			{"type": "input_text", "text": prompt},
		}
		if hasImageUpload {
			content = append(content, map[string]any{
				"type":      "input_image",
				"image_url": imageDataURL,
			})
		}
		return json.Marshal(map[string]any{
			"instructions": "你是一个有帮助的助手。",
			"input": []map[string]any{
				{
					"role":    "user",
					"content": content,
				},
			},
			"store":  false,
			"stream": stream,
		})
	case consts.StyleAnthropic:
		if endpoint != "messages" {
			return nil, fmt.Errorf("Anthropic 仅支持 messages 端点")
		}
		content := []map[string]any{
			{"type": "text", "text": prompt},
		}
		if hasImageUpload {
			content = append(content, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": imageMimeType,
					"data":       imageBase64,
				},
			})
		}
		return json.Marshal(map[string]any{
			"max_tokens": 1024,
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": content,
				},
			},
			"stream": stream,
		})
	case consts.StyleGemini:
		if endpoint != "chat/completions" {
			return nil, fmt.Errorf("Gemini 仅支持 chat/completions 端点")
		}
		parts := []map[string]any{
			{"text": prompt},
		}
		if hasImageUpload {
			parts = append(parts, map[string]any{
				"inlineData": map[string]any{
					"mimeType": imageMimeType,
					"data":     imageBase64,
				},
			})
		}
		return json.Marshal(map[string]any{
			"contents": []map[string]any{
				{
					"parts": parts,
				},
			},
		})
	default:
		return nil, fmt.Errorf("unsupported provider type")
	}
}

func detectImageMIMEType(fileName string, imageBytes []byte) string {
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(fileName)))
	if ext != "" {
		if m := mime.TypeByExtension(ext); strings.HasPrefix(strings.ToLower(m), "image/") {
			return m
		}
	}
	if len(imageBytes) > 0 {
		if m := strings.ToLower(http.DetectContentType(imageBytes)); strings.HasPrefix(m, "image/") {
			return m
		}
	}
	return "image/png"
}

func enrichTestRequestBodyForLog(body []byte, model string) ([]byte, error) {
	if gjson.GetBytes(body, "model").Exists() {
		return body, nil
	}
	return sjson.SetBytes(body, "model", model)
}

func buildImageEditLogBody(chatModel *ChatModel, req ChatTestRequest, imageFileName string, maskFileName string, hasImageUpload bool, hasMaskUpload bool) ([]byte, error) {
	payload := map[string]any{
		"model":            chatModel.Model,
		"prompt":           strings.TrimSpace(req.Prompt),
		"endpoint":         "images/edits",
		"size":             strings.TrimSpace(req.Size),
		"image_url":        strings.TrimSpace(req.ImageURL),
		"mask_url":         strings.TrimSpace(req.MaskURL),
		"image_file_name":  strings.TrimSpace(imageFileName),
		"mask_file_name":   strings.TrimSpace(maskFileName),
		"has_image_upload": hasImageUpload,
		"has_mask_upload":  hasMaskUpload,
	}
	return json.Marshal(payload)
}

func resolveTestLogBeforer(style string, endpoint string) service.Beforer {
	switch style {
	case consts.StyleOpenAI:
		switch endpoint {
		case "images/generations", "images/edits", "videos":
			return service.BeforerOpenAIMedia
		default:
			return service.BeforerOpenAI
		}
	case consts.StyleOpenAIRes:
		return service.BeforerOpenAIRes
	case consts.StyleAnthropic:
		return service.BeforerAnthropic
	case consts.StyleGemini:
		return service.BeforerOpenAI
	default:
		return nil
	}
}

func resolveTestLogProcesser(style string) service.Processer {
	switch style {
	case consts.StyleOpenAI, consts.StyleGemini:
		return service.ProcesserOpenAI
	case consts.StyleOpenAIRes:
		return service.ProcesserOpenAiRes
	case consts.StyleAnthropic:
		return service.ProcesserAnthropic
	default:
		return nil
	}
}

func recordTestChatSuccess(ctx context.Context, c *gin.Context, chatModel *ChatModel, style string, endpoint string, startReq time.Time, requestBody []byte, responseBody []byte) error {
	logID, err := service.SaveChatLog(ctx, models.ChatLog{
		Name:                chatModel.LogicalModelName(),
		ProviderModel:       chatModel.Model,
		ProviderName:        chatModel.DisplayProviderName(),
		ModelWithProviderID: chatModel.ModelWithProviderID,
		Status:              "success",
		Style:               style,
		UserAgent:           c.Request.UserAgent(),
		RemoteIP:            c.ClientIP(),
		ChatIO:              1,
		ProxyTimeMs:         int(time.Since(startReq).Milliseconds()),
	})
	if err != nil {
		return err
	}

	beforer := resolveTestLogBeforer(style, endpoint)
	processer := resolveTestLogProcesser(style)
	if beforer == nil || processer == nil {
		return saveTestChatIO(ctx, logID, requestBody, string(responseBody))
	}

	before, err := beforer(requestBody)
	if err != nil {
		return saveTestChatIO(ctx, logID, requestBody, string(responseBody))
	}

	service.RecordLog(ctx, startReq, 0, io.NopCloser(bytes.NewReader(responseBody)), processer, logID, 0, *before, true, style)
	return nil
}

func recordTestChatFailure(ctx context.Context, c *gin.Context, chatModel *ChatModel, style string, startReq time.Time, requestBody []byte, errText string) {
	if chatModel == nil {
		return
	}

	logID, err := service.SaveChatLog(ctx, models.ChatLog{
		Name:                chatModel.LogicalModelName(),
		ProviderModel:       chatModel.Model,
		ProviderName:        chatModel.DisplayProviderName(),
		ModelWithProviderID: chatModel.ModelWithProviderID,
		Status:              "error",
		Style:               style,
		UserAgent:           c.Request.UserAgent(),
		RemoteIP:            c.ClientIP(),
		ChatIO:              1,
		Error:               errText,
		ProxyTimeMs:         int(time.Since(startReq).Milliseconds()),
	})
	if err != nil {
		slog.Error("保存测试场失败日志失败", "error", err, "model", chatModel.ModelName, "provider", chatModel.ProviderName)
		return
	}

	if err := saveTestChatIO(ctx, logID, requestBody, errText); err != nil {
		slog.Error("保存测试场失败日志 IO 失败", "error", err, "log_id", logID)
	}
}

func saveTestChatIO(ctx context.Context, logID uint, requestBody []byte, output string) error {
	return gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{
		LogId:        logID,
		Input:        string(requestBody),
		OutputString: output,
	})
}

// decodeHTTPResponseBody 根据响应头自动解压响应体，目前支持 gzip。
func decodeHTTPResponseBody(header http.Header, body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Encoding")))
	isGzip := strings.Contains(encoding, "gzip") || (len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b)
	if !isGzip {
		return body
	}

	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer reader.Close()

	decoded, err := io.ReadAll(reader)
	if err != nil {
		return body
	}
	return decoded
}

func extractChatContent(style string, endpoint string, raw []byte) string {
	switch style {
	case consts.StyleOpenAI:
		if endpoint == "images/generations" || endpoint == "images/edits" {
			if url := gjson.GetBytes(raw, "data.0.url"); url.Exists() && url.String() != "" {
				return "已生成图片（URL）"
			}
			if b64 := gjson.GetBytes(raw, "data.0.b64_json"); b64.Exists() {
				return fmt.Sprintf("已生成图片（base64，长度 %d）", len(b64.String()))
			}
			return ""
		}
		if endpoint == "videos" {
			if url := gjson.GetBytes(raw, "data.0.url"); url.Exists() && url.String() != "" {
				return url.String()
			}
			return ""
		}
		if endpoint == "responses" {
			if out := gjson.GetBytes(raw, "output_text"); out.Exists() && out.String() != "" {
				return out.String()
			}
			parts := gjson.GetBytes(raw, "output.#.content.#.text").Array()
			return joinTextParts(parts)
		}
		if content := gjson.GetBytes(raw, "choices.0.message.content"); content.Exists() {
			if content.Type == gjson.String {
				return content.String()
			}
			parts := gjson.GetBytes(raw, "choices.0.message.content.#.text").Array()
			return joinTextParts(parts)
		}
		return ""
	case consts.StyleOpenAIRes:
		if out := gjson.GetBytes(raw, "output_text"); out.Exists() && out.String() != "" {
			return out.String()
		}
		parts := gjson.GetBytes(raw, "output.#.content.#.text").Array()
		return joinTextParts(parts)
	case consts.StyleAnthropic:
		parts := gjson.GetBytes(raw, "content.#.text").Array()
		return joinTextParts(parts)
	case consts.StyleGemini:
		parts := gjson.GetBytes(raw, "candidates.0.content.parts.#.text").Array()
		return joinTextParts(parts)
	default:
		return ""
	}
}

func extractChatImagePreview(style string, endpoint string, raw []byte) string {
	if style != consts.StyleOpenAI {
		return ""
	}
	if endpoint != "images/generations" && endpoint != "images/edits" {
		return ""
	}

	if url := strings.TrimSpace(gjson.GetBytes(raw, "data.0.url").String()); url != "" {
		return url
	}

	b64 := strings.TrimSpace(gjson.GetBytes(raw, "data.0.b64_json").String())
	if b64 == "" {
		return ""
	}
	mimeType := strings.TrimSpace(gjson.GetBytes(raw, "data.0.mime_type").String())
	if mimeType == "" {
		mimeType = "image/png"
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
}

func normalizeTestEndpoint(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "chat/completions":
		return "chat/completions"
	case "responses":
		return "responses"
	case "messages":
		return "messages"
	case "images/generations":
		return "images/generations"
	case "images/edits":
		return "images/edits"
	case "videos":
		return "videos"
	default:
		return ""
	}
}

func isStreamableTestEndpoint(endpoint string) bool {
	switch endpoint {
	case "chat/completions", "messages", "responses":
		return true
	default:
		return false
	}
}

type streamFlushWriter struct {
	writer gin.ResponseWriter
}

func (w *streamFlushWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if err != nil {
		return n, err
	}
	w.writer.Flush()
	return n, nil
}

func buildOpenAIImageEditRequest(ctx context.Context, chatModel *ChatModel, header http.Header, req ChatTestRequest) (*http.Request, error) {
	if strings.TrimSpace(req.ImageURL) == "" {
		return nil, fmt.Errorf("images/edits 需要 image_url")
	}

	var config providers.OpenAI
	if err := json.Unmarshal([]byte(chatModel.Config), &config); err != nil {
		return nil, fmt.Errorf("invalid openai config")
	}
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("openai config 缺少 base_url 或 api_key")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writer.WriteField("prompt", strings.TrimSpace(req.Prompt)); err != nil {
		return nil, err
	}
	if err := writer.WriteField("model", chatModel.Model); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Size) != "" {
		if err := writer.WriteField("size", strings.TrimSpace(req.Size)); err != nil {
			return nil, err
		}
	}

	imageBytes, imageName, err := fetchRemoteFile(ctx, strings.TrimSpace(req.ImageURL))
	if err != nil {
		return nil, err
	}
	if err := writeMultipartFile(writer, "image", imageName, imageBytes); err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.MaskURL) != "" {
		maskBytes, maskName, err := fetchRemoteFile(ctx, strings.TrimSpace(req.MaskURL))
		if err != nil {
			return nil, err
		}
		if err := writeMultipartFile(writer, "mask", maskName, maskBytes); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	base := strings.TrimRight(config.BaseURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/images/edits", base), body)
	if err != nil {
		return nil, err
	}
	if header != nil {
		request.Header = header.Clone()
	} else {
		request.Header = http.Header{}
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.APIKey))
	return request, nil
}

func buildOpenAIImageEditRequestFromUpload(
	ctx context.Context,
	chatModel *ChatModel,
	header http.Header,
	req ChatTestRequest,
	imageName string,
	imageBytes []byte,
	maskName string,
	maskBytes []byte,
) (*http.Request, error) {
	var config providers.OpenAI
	if err := json.Unmarshal([]byte(chatModel.Config), &config); err != nil {
		return nil, fmt.Errorf("invalid openai config")
	}
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("openai config 缺少 base_url 或 api_key")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if err := writer.WriteField("prompt", strings.TrimSpace(req.Prompt)); err != nil {
		return nil, err
	}
	if err := writer.WriteField("model", chatModel.Model); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Size) != "" {
		if err := writer.WriteField("size", strings.TrimSpace(req.Size)); err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(imageName) == "" {
		imageName = "image"
	}
	if err := writeMultipartFile(writer, "image", imageName, imageBytes); err != nil {
		return nil, err
	}

	if len(maskBytes) > 0 {
		if strings.TrimSpace(maskName) == "" {
			maskName = "mask"
		}
		if err := writeMultipartFile(writer, "mask", maskName, maskBytes); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	base := strings.TrimRight(config.BaseURL, "/")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/images/edits", base), body)
	if err != nil {
		return nil, err
	}
	if header != nil {
		request.Header = header.Clone()
	} else {
		request.Header = http.Header{}
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", config.APIKey))
	return request, nil
}

func fetchRemoteFile(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Timeout: time.Second * 20}
	res, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载资源失败: %s", res.Status)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	name := path.Base(req.URL.Path)
	if name == "" || name == "/" || name == "." {
		name = "image"
	}
	return data, name, nil
}

func writeMultipartFile(writer *multipart.Writer, field, filename string, content []byte) error {
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	_, err = part.Write(content)
	return err
}

func extractChatContentFromSSE(style string, endpoint string, raw []byte) string {
	payload := string(raw)
	if !strings.Contains(payload, "data:") {
		return ""
	}
	lines := strings.Split(payload, "\n")
	builder := strings.Builder{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		appendSSEText(&builder, style, endpoint, []byte(data))
	}
	return builder.String()
}

func appendSSEText(builder *strings.Builder, style string, endpoint string, raw []byte) {
	switch style {
	case consts.StyleOpenAI:
		if endpoint == "responses" {
			parts := gjson.GetBytes(raw, "output.#.content.#.text").Array()
			appendTextParts(builder, parts)
			return
		}
		part := gjson.GetBytes(raw, "choices.0.delta.content")
		if part.Exists() && part.String() != "" {
			appendText(builder, part.String())
		}
	case consts.StyleOpenAIRes:
		parts := gjson.GetBytes(raw, "output.#.content.#.text").Array()
		appendTextParts(builder, parts)
	case consts.StyleAnthropic:
		part := gjson.GetBytes(raw, "delta.text")
		if part.Exists() && part.String() != "" {
			appendText(builder, part.String())
		}
	case consts.StyleGemini:
		parts := gjson.GetBytes(raw, "candidates.0.content.parts.#.text").Array()
		appendTextParts(builder, parts)
	default:
		return
	}
}

func appendTextParts(builder *strings.Builder, parts []gjson.Result) {
	for _, part := range parts {
		text := part.String()
		if strings.TrimSpace(text) == "" {
			continue
		}
		appendText(builder, text)
	}
}

func appendText(builder *strings.Builder, text string) {
	if text == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteString("")
	}
	builder.WriteString(text)
}

func joinTextParts(parts []gjson.Result) string {
	if len(parts) == 0 {
		return ""
	}
	builder := strings.Builder{}
	for _, part := range parts {
		text := strings.TrimSpace(part.String())
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(text)
	}
	return builder.String()
}

func TestReactHandler(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if id == "" {
		common.BadRequest(c, "Invalid ID format")
		return
	}

	chatModel, err := FindChatModel(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "ModelWithProvider not found")
			return
		}
		common.InternalServerError(c, "Database error")
		return
	}

	if providers.ResolveStyle("", chatModel.Config) != consts.StyleOpenAI {
		c.SSEvent("error", "该测试仅支持 OpenAI 类型")
		return
	}

	var config providers.OpenAI
	if err := json.Unmarshal([]byte(chatModel.Config), &config); err != nil {
		common.ErrorWithHttpStatus(c, http.StatusBadRequest, 400, "Invalid config format")
		return
	}

	client := openai.NewClient(
		option.WithBaseURL(config.BaseURL),
		option.WithAPIKey(config.APIKey),
	)

	agent := react.New(client, 20)
	question := "分两次获取一下南京和北京的天气 每次调用后回复我对应城市的总结信息"
	model := chatModel.Model

	tools := []openai.ChatCompletionToolUnionParam{
		openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        "get_weather",
			Description: openai.String("Get weather at the given location"),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]string{
						"type":        "string",
						"description": "The city name",
					},
				},
				"required": []string{"location"},
			},
		}),
	}
	var checkError error
	var toolCount int
	var nankingCount int
	var pekingCount int

	c.SSEvent("start", fmt.Sprintf("提供商:%s 模型:%s 问题:%s", chatModel.Name, chatModel.Model, question))
	start := time.Now()
	for content, err := range agent.RunStream(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(question),
		},
		Tools: tools,
		Model: model,
	}, GetWeather) {
		if err != nil {
			c.SSEvent("error", err.Error())
			break
		}
		var res string
		switch content.Cate {
		case "message":
			if len(content.Chunk.Choices) > 0 {
				res = content.Chunk.Choices[0].Delta.Content
			}
		case "toolcall":
			data, err := json.Marshal(content.ToolCall.Function)
			if err != nil {
				c.SSEvent("error", err.Error())
				break
			}
			res = string(data)
			location := gjson.Get(content.ToolCall.Function.Arguments, "location").String()
			if location == "南京" {
				nankingCount++
			}
			if location == "北京" {
				pekingCount++
			}
			if content.Step == 0 && location != "南京" {
				checkError = errors.New("第一次应选择南京")
			}
			if content.Step == 1 && location != "北京" {
				checkError = errors.New("第二次应选择北京")
			}
			toolCount++
		case "toolres":
			data, err := json.Marshal(content.ToolRes)
			if err != nil {
				c.SSEvent("error", err.Error())
				break
			}
			res = string(data)
		}
		c.SSEvent(content.Cate, res)
		c.Writer.Flush()
	}
	if toolCount != 2 || nankingCount != 1 || pekingCount != 1 {
		checkError = fmt.Errorf("工具调用次数异常: 南京: %d 北京: %d 总计: %d", nankingCount, pekingCount, toolCount)
	}

	if checkError != nil {
		c.SSEvent("error", checkError.Error())
		c.Writer.Flush()
		return
	}
	c.SSEvent("success", fmt.Sprintf("成功通过测试, 耗时: %.2fs", time.Since(start).Seconds()))
}

func mergeHeaders(dst http.Header, extra http.Header, skipKeys map[string]struct{}) {
	for key, values := range extra {
		if len(values) == 0 {
			continue
		}
		if _, skip := skipKeys[strings.ToLower(key)]; skip {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
}

func GetWeather(ctx context.Context, call openai.ChatCompletionChunkChoiceDeltaToolCallFunction) (*openai.ChatCompletionToolMessageParamContentUnion, error) {
	if call.Name != "get_weather" {
		return nil, fmt.Errorf("invalid tool call name: %s", call.Name)
	}
	location := gjson.Get(call.Arguments, "location")
	var res string
	switch location.String() {
	case "南京":
		res = "南京天气晴转多云，温度 18℃"
	case "北京":
		res = "北京天气大雨转小雨，温度 15℃"
	default:
		res = "暂不支持该地区天气查询"
	}
	return &openai.ChatCompletionToolMessageParamContentUnion{
		OfString: openai.String(res),
	}, nil
}

type ChatModel struct {
	Name                string            `json:"name"`
	ModelWithProviderID uint              `json:"model_with_provider_id"`
	ProviderName        string            `json:"provider_name"`
	ModelName           string            `json:"model_name"`
	Model               string            `json:"model"`
	Config              string            `json:"config"`
	ProxyURL            string            `json:"proxy_url"`
	WithHeader          *bool             `json:"with_header,omitempty"`
	CustomerHeaders     map[string]string `json:"customer_headers,omitempty"`
}

func (c *ChatModel) LogicalModelName() string {
	if strings.TrimSpace(c.ModelName) != "" {
		return c.ModelName
	}
	return c.Model
}

func (c *ChatModel) DisplayProviderName() string {
	if strings.TrimSpace(c.ProviderName) != "" {
		return c.ProviderName
	}
	return c.Name
}

func FindChatModel(ctx context.Context, id string) (*ChatModel, error) {
	// Get ModelWithProvider by ID
	modelWithProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		return nil, err
	}

	model, err := gorm.G[models.Model](models.DB).Where("id = ?", modelWithProvider.ModelID).First(ctx)
	if err != nil {
		return nil, err
	}

	// Get the Provider
	provider, err := gorm.G[models.Provider](models.DB).Where("id = ?", modelWithProvider.ProviderID).First(ctx)
	if err != nil {
		return nil, err
	}

	// Convert WithHeader from int to *bool
	var withHeader *bool
	if modelWithProvider.WithHeader == 1 {
		withHeader = &[]bool{true}[0]
	} else {
		withHeader = &[]bool{false}[0]
	}

	// Convert CustomerHeaders from JSON string to map
	var customerHeaders map[string]string
	if modelWithProvider.CustomerHeaders != "" {
		if err := json.Unmarshal([]byte(modelWithProvider.CustomerHeaders), &customerHeaders); err != nil {
			// If JSON parsing fails, initialize empty map
			customerHeaders = make(map[string]string)
		}
	} else {
		customerHeaders = make(map[string]string)
	}

	return &ChatModel{
		Name:                provider.Name,
		ModelWithProviderID: modelWithProvider.ID,
		ProviderName:        provider.Name,
		ModelName:           model.Name,
		Model:               modelWithProvider.ProviderModel,
		Config:              provider.Config,
		ProxyURL:            provider.ProxyURL,
		WithHeader:          withHeader,
		CustomerHeaders:     customerHeaders,
	}, nil
}
