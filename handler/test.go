package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
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
	"github.com/racio/orvion/service/subscription"
	"github.com/tidwall/gjson"
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
	providerInstance, err := providers.New(chatModel.Type, chatModel.Config)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
		return
	}

	// Test connectivity by fetching models
	responseHeaderTimeout := time.Second * time.Duration(30)
	var testBody []byte
	switch chatModel.Type {
	case consts.StyleOpenAI:
		testBody = []byte(testOpenAI)
	case consts.StyleAnthropic:
		testBody = []byte(testAnthropic)
	case consts.StyleOpenAIRes, consts.StyleCodexAuths:
		testBody = []byte(testOpenAIRes)
	case consts.StyleIFlowAuths:
		testBody = []byte(testOpenAI)
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
		"authorization":  {},
		"content-length": {},
		"host":           {},
	})
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

	if res.StatusCode != http.StatusOK {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, fmt.Sprintf("code: %d body: %s", res.StatusCode, string(content)))
		return
	}

	common.SuccessWithMessage(c, string(content), nil)
}

type ChatTestRequest struct {
	Prompt   string `json:"prompt"`
	Endpoint string `json:"endpoint"`
	ImageURL string `json:"image_url"`
	MaskURL  string `json:"mask_url"`
	Size     string `json:"size"`
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

	providerInstance, err := providers.New(chatModel.Type, chatModel.Config)
	if err != nil {
		common.BadRequest(c, "Failed to create provider: "+err.Error())
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
		"authorization":  {},
		"content-length": {},
		"host":           {},
	})

	var request *http.Request
	if endpoint == "images/edits" {
		if chatModel.Type != consts.StyleOpenAI && chatModel.Type != consts.StyleIFlowAuths && chatModel.Type != consts.StyleIFlow {
			common.BadRequest(c, "images/edits 仅支持 OpenAI 兼容提供商")
			return
		}
		if isMultipart {
			if len(imageBytes) == 0 {
				common.BadRequest(c, "images/edits 需要上传 image 文件")
				return
			}
			request, err = buildOpenAIImageEditRequestFromUpload(ctx, chatModel, header, req, imageFileName, imageBytes, maskFileName, maskBytes)
		} else {
			request, err = buildOpenAIImageEditRequest(ctx, chatModel, header, req)
		}
	} else {
		body, err := buildChatTestBody(chatModel.Type, endpoint, req)
		if err != nil {
			common.BadRequest(c, err.Error())
			return
		}
		ctx = context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, endpoint)
		request, err = providerInstance.BuildReq(ctx, header, chatModel.Model, body)
	}
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to build request: "+err.Error())
		return
	}

	client := &http.Client{Timeout: time.Second * 60}
	res, err := client.Do(request)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, 502, "Failed to send request: "+err.Error())
		return
	}
	defer res.Body.Close()

	content, err := io.ReadAll(res.Body)
	if err != nil {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, "Failed to read response: "+err.Error())
		return
	}

	if res.StatusCode != http.StatusOK {
		common.ErrorWithHttpStatus(c, http.StatusOK, res.StatusCode, fmt.Sprintf("code: %d body: %s", res.StatusCode, string(content)))
		return
	}

	text := extractChatContent(chatModel.Type, endpoint, content)
	if strings.TrimSpace(text) == "" {
		text = extractChatContentFromSSE(chatModel.Type, endpoint, content)
	}
	if strings.TrimSpace(text) == "" {
		text = string(content)
	}

	common.Success(c, map[string]string{
		"content": text,
	})
}

func buildChatTestBody(style string, endpoint string, req ChatTestRequest) ([]byte, error) {
	prompt := strings.TrimSpace(req.Prompt)
	switch style {
	case consts.StyleOpenAI, consts.StyleIFlowAuths:
		switch endpoint {
		case "chat/completions":
			return json.Marshal(map[string]any{
				"messages": []map[string]string{
					{"role": "user", "content": prompt},
				},
				"temperature": 0.2,
			})
		case "responses":
			return json.Marshal(map[string]any{
				"instructions": "你是一个有帮助的助手。",
				"input": []map[string]any{
					{
						"role": "user",
						"content": []map[string]string{
							{"type": "input_text", "text": prompt},
						},
					},
				},
				"store": false,
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
	case consts.StyleOpenAIRes, consts.StyleCodexAuths:
		if endpoint != "responses" {
			return nil, fmt.Errorf("该提供商仅支持 responses 端点")
		}
		return json.Marshal(map[string]any{
			"instructions": "你是一个有帮助的助手。",
			"input": []map[string]any{
				{
					"role": "user",
					"content": []map[string]string{
						{"type": "input_text", "text": prompt},
					},
				},
			},
			"store": false,
		})
	case consts.StyleAnthropic:
		if endpoint != "messages" {
			return nil, fmt.Errorf("Anthropic 仅支持 messages 端点")
		}
		return json.Marshal(map[string]any{
			"max_tokens": 1024,
			"messages": []map[string]any{
				{
					"role": "user",
					"content": []map[string]string{
						{"type": "text", "text": prompt},
					},
				},
			},
		})
	case consts.StyleGemini:
		if endpoint != "chat/completions" {
			return nil, fmt.Errorf("Gemini 仅支持 chat/completions 端点")
		}
		return json.Marshal(map[string]any{
			"contents": []map[string]any{
				{
					"parts": []map[string]string{
						{"text": prompt},
					},
				},
			},
		})
	default:
		return nil, fmt.Errorf("unsupported provider type")
	}
}

func extractChatContent(style string, endpoint string, raw []byte) string {
	switch style {
	case consts.StyleOpenAI, consts.StyleIFlowAuths:
		if endpoint == "images/generations" || endpoint == "images/edits" {
			if url := gjson.GetBytes(raw, "data.0.url"); url.Exists() && url.String() != "" {
				return url.String()
			}
			if b64 := gjson.GetBytes(raw, "data.0.b64_json"); b64.Exists() {
				return fmt.Sprintf("已返回 base64 图像数据（长度 %d）", len(b64.String()))
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
	case consts.StyleOpenAIRes, consts.StyleCodexAuths:
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
	case consts.StyleOpenAI, consts.StyleIFlowAuths:
		if endpoint == "responses" {
			parts := gjson.GetBytes(raw, "output.#.content.#.text").Array()
			appendTextParts(builder, parts)
			return
		}
		part := gjson.GetBytes(raw, "choices.0.delta.content")
		if part.Exists() && part.String() != "" {
			appendText(builder, part.String())
		}
	case consts.StyleOpenAIRes, consts.StyleCodexAuths:
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

	if chatModel.Type != consts.StyleOpenAI {
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
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	Model           string            `json:"model"`
	Config          string            `json:"config"`
	WithHeader      *bool             `json:"with_header,omitempty"`
	CustomerHeaders map[string]string `json:"customer_headers,omitempty"`
}

func FindChatModel(ctx context.Context, id string) (*ChatModel, error) {
	// Get ModelWithProvider by ID
	modelWithProvider, err := gorm.G[models.ModelWithProvider](models.DB).Where("id = ?", id).First(ctx)
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

	resolvedConfig, err := subscription.ResolveProviderConfigForRequest(provider.ID, provider.Type, provider.Config)
	if err != nil {
		return nil, err
	}

	return &ChatModel{
		Name:            provider.Name,
		Type:            provider.Type,
		Model:           modelWithProvider.ProviderModel,
		Config:          resolvedConfig,
		WithHeader:      withHeader,
		CustomerHeaders: customerHeaders,
	}, nil
}
