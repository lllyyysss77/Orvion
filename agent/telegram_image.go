package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/tidwall/gjson"
)

const (
	telegramAgentImageGenerationTimeout = 180 * time.Second
	telegramAgentImageResponseMaxBytes  = 32 << 20
)

type telegramAgentGeneratedImage struct {
	Source  string
	Caption string
	Cleanup func()
}

var runTelegramAgentImageGenerationFunc = runTelegramAgentImageGeneration

func runTelegramAgentImageGeneration(ctx context.Context, client TelegramClient, chatID int64, prompt string, cfg models.TelegramAgentConfig, loadHistory bool) error {
	placeholderID, err := client.SendMessage(ctx, chatID, "正在生成图片...")
	if err != nil {
		return err
	}
	stopTyping := startTelegramTypingLoop(ctx, client, chatID)
	defer stopTyping()

	history := make([]chatMessage, 0)
	if loadHistory {
		history, err = loadTelegramSessionMessages(ctx, chatID, cfg)
		if err != nil {
			return err
		}
	}

	images, err := generateTelegramAgentImages(ctx, cfg, prompt)
	if err != nil {
		errorText := "生图失败：" + err.Error()
		if editErr := client.EditMessage(ctx, chatID, placeholderID, trimTelegramMessage(errorText)); editErr != nil {
			return fmt.Errorf("%v; 编辑失败消息也失败: %w", err, editErr)
		}
		return err
	}
	defer cleanupTelegramAgentGeneratedImages(images)

	finalAnswer := "图片已生成。"
	if err := client.EditMessage(ctx, chatID, placeholderID, finalAnswer); err != nil {
		return err
	}
	if err := sendTelegramAgentGeneratedImages(ctx, client, chatID, images, prompt); err != nil {
		return err
	}

	if loadHistory {
		historyPrompt := strings.TrimSpace(prompt)
		nextHistory := trimTelegramHistory(append(history,
			chatMessage{Role: "user", Content: historyPrompt},
			chatMessage{Role: "assistant", Content: finalAnswer},
		), cfg)
		session := getTelegramSession(chatID)
		session.messages = nextHistory
		if err := saveTelegramSessionMessages(ctx, chatID, nextHistory); err != nil {
			return err
		}
		scheduleTelegramAgentMemoryExtraction(cfg, historyPrompt, finalAnswer, time.Now())
	}
	return nil
}

func generateTelegramAgentImages(ctx context.Context, cfg models.TelegramAgentConfig, prompt string) ([]telegramAgentGeneratedImage, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("生图提示词不能为空")
	}
	if strings.TrimSpace(cfg.ImageModel) == "" {
		return nil, errors.New("未配置 TG Agent 生图模型")
	}

	pool, err := buildTelegramAgentImageProviderPool(cfg)
	if err != nil {
		return nil, err
	}

	startedAt := time.Now()
	attempt, err := performTelegramAgentProviderRequestWithRetry(ctx, pool, false, startedAt, func(requestCtx context.Context, selected selectedModelProvider) ([]byte, context.Context, error) {
		body, buildErr := buildTelegramAgentImageRequestBody(selected.ProviderModel, prompt)
		return body, context.WithValue(requestCtx, consts.ContextKeyOpenAIEndpoint, "images/generations"), buildErr
	})
	if err != nil {
		return nil, err
	}
	defer attempt.Response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(attempt.Response.Body, telegramAgentImageResponseMaxBytes))
	if err != nil {
		return nil, err
	}
	images, err := parseTelegramAgentImageGenerationResponse(raw, prompt)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, errors.New("上游未返回可发送的图片")
	}
	return images, nil
}

func buildTelegramAgentImageProviderPool(cfg models.TelegramAgentConfig) (telegramAgentModelProviderPool, error) {
	baseURL, err := normalizeTelegramAgentDirectBaseURL(cfg.BaseURL)
	if err != nil {
		return telegramAgentModelProviderPool{}, err
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	modelName := strings.TrimSpace(cfg.ImageModel)
	if apiKey == "" {
		return telegramAgentModelProviderPool{}, errors.New("TG Agent 直连 API Key 不能为空")
	}
	if modelName == "" {
		return telegramAgentModelProviderPool{}, errors.New("TG Agent 生图模型名不能为空")
	}

	providerConfig, err := json.Marshal(map[string]string{
		"base_url": baseURL,
		"api_key":  apiKey,
	})
	if err != nil {
		return telegramAgentModelProviderPool{}, err
	}

	selected := selectedModelProvider{
		ModelName:       modelName,
		ProviderModel:   modelName,
		ProviderName:    "TG Agent 生图直连",
		ProviderConfig:  string(providerConfig),
		ProviderStyle:   consts.StyleOpenAI,
		ClientStyle:     consts.StyleOpenAI,
		TimeoutSeconds:  int(telegramAgentImageGenerationTimeout / time.Second),
		CustomerHeaders: map[string]string{},
	}

	const directProviderID uint = 1
	return telegramAgentModelProviderPool{
		ModelName:   modelName,
		Candidates:  map[uint]selectedModelProvider{directProviderID: selected},
		WeightItems: map[uint]int{directProviderID: 1},
		MaxRetry:    3,
		Strategy:    consts.BalancerRotor,
		Breaker:     false,
	}, nil
}

func buildTelegramAgentImageRequestBody(model string, prompt string) ([]byte, error) {
	payload := map[string]any{
		"model":  strings.TrimSpace(model),
		"prompt": strings.TrimSpace(prompt),
		"n":      1,
		"extra_body": map[string]any{
			"response_format": "b64_json",
		},
	}
	return json.Marshal(payload)
}

func parseTelegramAgentImageGenerationResponse(raw []byte, prompt string) ([]telegramAgentGeneratedImage, error) {
	if !gjson.ValidBytes(raw) {
		return nil, errors.New("生图响应不是合法 JSON")
	}

	images := make([]telegramAgentGeneratedImage, 0, 2)
	for _, item := range gjson.GetBytes(raw, "data").Array() {
		if image, ok, err := telegramAgentGeneratedImageFromJSON(item, prompt); err != nil {
			cleanupTelegramAgentGeneratedImages(images)
			return nil, err
		} else if ok {
			images = append(images, image)
		}
	}
	if len(images) == 0 {
		for _, path := range []string{"url", "result.url", "output.0.url", "image_url.url", "images.0.url"} {
			if image, ok, err := telegramAgentGeneratedImageFromURL(gjson.GetBytes(raw, path).String(), prompt, ""); err != nil {
				cleanupTelegramAgentGeneratedImages(images)
				return nil, err
			} else if ok {
				images = append(images, image)
			}
		}
	}
	return images, nil
}

func telegramAgentGeneratedImageFromJSON(item gjson.Result, prompt string) (telegramAgentGeneratedImage, bool, error) {
	if image, ok, err := telegramAgentGeneratedImageFromURL(item.Get("url").String(), prompt, item.Get("revised_prompt").String()); err != nil || ok {
		return image, ok, err
	}
	if image, ok, err := telegramAgentGeneratedImageFromURL(item.Get("image_url.url").String(), prompt, item.Get("revised_prompt").String()); err != nil || ok {
		return image, ok, err
	}
	b64 := strings.TrimSpace(item.Get("b64_json").String())
	if b64 == "" {
		return telegramAgentGeneratedImage{}, false, nil
	}
	mimeType := strings.TrimSpace(item.Get("mime_type").String())
	if mimeType == "" {
		mimeType = "image/png"
	}
	return telegramAgentGeneratedImageFromBase64(b64, mimeType, prompt, item.Get("revised_prompt").String())
}

func telegramAgentGeneratedImageFromURL(rawURL string, prompt string, revisedPrompt string) (telegramAgentGeneratedImage, bool, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return telegramAgentGeneratedImage{}, false, nil
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "data:image/") {
		image, err := telegramAgentGeneratedImageFromDataURL(rawURL, prompt, revisedPrompt)
		return image, true, err
	}
	return telegramAgentGeneratedImage{
		Source:  rawURL,
		Caption: telegramAgentImageCaption(prompt, revisedPrompt),
	}, true, nil
}

func telegramAgentGeneratedImageFromDataURL(dataURL string, prompt string, revisedPrompt string) (telegramAgentGeneratedImage, error) {
	head, b64, ok := strings.Cut(dataURL, ",")
	if !ok {
		return telegramAgentGeneratedImage{}, errors.New("图片 data URL 格式无效")
	}
	mimeType := "image/png"
	if strings.HasPrefix(strings.ToLower(head), "data:") {
		mimeType = strings.TrimSpace(strings.Split(strings.TrimPrefix(head, "data:"), ";")[0])
	}
	image, _, err := telegramAgentGeneratedImageFromBase64(b64, mimeType, prompt, revisedPrompt)
	return image, err
}

func telegramAgentGeneratedImageFromBase64(b64 string, mimeType string, prompt string, revisedPrompt string) (telegramAgentGeneratedImage, bool, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return telegramAgentGeneratedImage{}, false, fmt.Errorf("解析 base64 图片失败: %w", err)
	}
	if len(data) == 0 {
		return telegramAgentGeneratedImage{}, false, errors.New("base64 图片为空")
	}

	ext := telegramAgentImageExtension(mimeType)
	file, err := os.CreateTemp("", "orvion-tg-image-*"+ext)
	if err != nil {
		return telegramAgentGeneratedImage{}, false, err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return telegramAgentGeneratedImage{}, false, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return telegramAgentGeneratedImage{}, false, err
	}

	return telegramAgentGeneratedImage{
		Source:  path,
		Caption: telegramAgentImageCaption(prompt, revisedPrompt),
		Cleanup: func() { _ = os.Remove(path) },
	}, true, nil
}

func sendTelegramAgentGeneratedImages(ctx context.Context, client TelegramClient, chatID int64, images []telegramAgentGeneratedImage, prompt string) error {
	attachmentClient, ok := client.(TelegramAttachmentClient)
	if !ok {
		_, err := client.SendMessage(ctx, chatID, "当前 Telegram 客户端不支持图片发送。")
		return err
	}
	for index, image := range images {
		source := strings.TrimSpace(image.Source)
		if source == "" {
			continue
		}
		caption := image.Caption
		if caption == "" {
			caption = telegramAgentImageCaption(prompt, "")
		}
		if len(images) > 1 {
			caption = fmt.Sprintf("%s\n%d/%d", caption, index+1, len(images))
		}
		if err := attachmentClient.SendPhoto(ctx, chatID, source, limitTelegramAgentAttachmentCaption(caption)); err != nil {
			return err
		}
	}
	return nil
}

func cleanupTelegramAgentGeneratedImages(images []telegramAgentGeneratedImage) {
	for _, image := range images {
		if image.Cleanup != nil {
			image.Cleanup()
		}
	}
}

func telegramAgentImageCaption(prompt string, revisedPrompt string) string {
	prompt = strings.TrimSpace(prompt)
	revisedPrompt = strings.TrimSpace(revisedPrompt)
	if revisedPrompt != "" && revisedPrompt != prompt {
		return "已生成图片\n提示词：" + revisedPrompt
	}
	if prompt != "" {
		return "已生成图片\n提示词：" + prompt
	}
	return "已生成图片"
}

func telegramAgentImageExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		ext := filepath.Ext(strings.ToLower(strings.TrimSpace(mimeType)))
		if ext != "" && len(ext) <= 6 {
			return ext
		}
		return ".png"
	}
}
