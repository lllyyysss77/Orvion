package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

type telegramTypingTestClient struct {
	typingCh chan int64
}

type telegramToolTestClient struct {
	sent   []string
	nextID int64
}

func (c telegramTypingTestClient) SendMessage(context.Context, int64, string) (int64, error) {
	return 1, nil
}

func (c telegramTypingTestClient) EditMessage(context.Context, int64, int64, string) error {
	return nil
}

func (c telegramTypingTestClient) SendTyping(_ context.Context, chatID int64) error {
	if c.typingCh == nil {
		return errors.New("typing channel is nil")
	}
	select {
	case c.typingCh <- chatID:
	default:
	}
	return nil
}

func (c *telegramToolTestClient) SendMessage(_ context.Context, _ int64, text string) (int64, error) {
	c.nextID++
	c.sent = append(c.sent, text)
	return c.nextID, nil
}

func (c *telegramToolTestClient) EditMessage(context.Context, int64, int64, string) error {
	return nil
}

func (c *telegramToolTestClient) SendTyping(context.Context, int64) error {
	return nil
}

func (c *telegramToolTestClient) lastSent() string {
	if len(c.sent) == 0 {
		return ""
	}
	return c.sent[len(c.sent)-1]
}

func TestStartTelegramTypingLoopSendsTyping(t *testing.T) {
	client := telegramTypingTestClient{typingCh: make(chan int64, 1)}
	stop := startTelegramTypingLoop(context.Background(), client, 12345)
	defer stop()

	select {
	case chatID := <-client.typingCh:
		if chatID != 12345 {
			t.Fatalf("期望 chat_id=12345，实际为 %d", chatID)
		}
	case <-time.After(time.Second):
		t.Fatalf("等待 TG typing 状态超时")
	}
}

func TestReadTelegramAgentStreamCollectsOpenAIUsage(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	var answer strings.Builder
	result, err := readTelegramAgentStream(context.Background(), consts.StyleOpenAI, strings.NewReader(stream), time.Now(), func(delta string) error {
		answer.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("readTelegramAgentStream 返回错误: %v", err)
	}
	if answer.String() != "你好" {
		t.Fatalf("期望流式增量为 你好，实际为 %q", answer.String())
	}
	if result.Usage.PromptTokens != 12 || result.Usage.CompletionTokens != 3 || result.Usage.TotalTokens != 15 {
		t.Fatalf("usage 解析不正确: %+v", result.Usage)
	}
	if result.Usage.CachedTokens != 4 {
		t.Fatalf("cached_tokens 解析不正确: %+v", result.Usage)
	}
}

func TestExtractTelegramAgentAnthropicUsage(t *testing.T) {
	start := `{"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":5}}}`
	delta := `{"type":"message_delta","usage":{"output_tokens":7}}`

	var usage = extractTelegramAgentAnthropicUsage(start)
	mergeTelegramAgentUsageMax(&usage, extractTelegramAgentAnthropicUsage(delta))

	if usage.PromptTokens != 20 || usage.CompletionTokens != 7 || usage.TotalTokens != 27 {
		t.Fatalf("Anthropic usage 解析不正确: %+v", usage)
	}
	if usage.CachedTokens != 5 {
		t.Fatalf("Anthropic cached_tokens 解析不正确: %+v", usage)
	}
}

func TestExtractTelegramAgentGeminiUsage(t *testing.T) {
	raw := `{"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4,"totalTokenCount":13,"cachedContentTokenCount":2}}`

	usage := extractTelegramAgentGeminiUsage(raw)

	if usage.PromptTokens != 9 || usage.CompletionTokens != 4 || usage.TotalTokens != 13 {
		t.Fatalf("Gemini usage 解析不正确: %+v", usage)
	}
	if usage.CachedTokens != 2 {
		t.Fatalf("Gemini cached_tokens 解析不正确: %+v", usage)
	}
}

func TestReadTelegramAgentOpenAIStreamWithToolsCollectsToolCalls(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"我先查一下。"}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"set_model_status","arguments":"{\"target\":\"claude"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\",\"enabled\":true,\"bulk\":true}"}}]}}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	result, err := readTelegramAgentOpenAIStreamWithTools(context.Background(), strings.NewReader(stream), time.Now(), func(delta string) error {
		t.Fatalf("工具调用流不应产生文本增量，实际为: %s", delta)
		return nil
	})
	if err != nil {
		t.Fatalf("readTelegramAgentOpenAIStreamWithTools 返回错误: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("期望收集 1 个工具调用，实际为 %d", len(result.ToolCalls))
	}
	call := result.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != telegramAgentToolSetModelStatus {
		t.Fatalf("工具调用基础信息不正确: %+v", call)
	}
	if call.Function.Arguments != `{"target":"claude","enabled":true,"bulk":true}` {
		t.Fatalf("工具调用参数拼接不正确: %s", call.Function.Arguments)
	}
	if result.Usage.TotalTokens != 15 {
		t.Fatalf("usage 聚合不正确: %+v", result.Usage)
	}
}

func TestReadTelegramAgentOpenAIStreamWithToolsFlushesContentOnlyWithoutToolCalls(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"最终"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"结论"}}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")

	var answer strings.Builder
	result, err := readTelegramAgentOpenAIStreamWithTools(context.Background(), strings.NewReader(stream), time.Now(), func(delta string) error {
		answer.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("readTelegramAgentOpenAIStreamWithTools 返回错误: %v", err)
	}
	if len(result.ToolCalls) != 0 {
		t.Fatalf("不应产生工具调用，实际为 %d", len(result.ToolCalls))
	}
	if answer.String() != "最终结论" {
		t.Fatalf("期望只在无工具调用时输出最终内容，实际为 %q", answer.String())
	}
}

func TestBuildTelegramAgentDirectProviderPoolUsesDirectConfig(t *testing.T) {
	previousDB := models.DB
	defer func() {
		models.DB = previousDB
	}()
	models.DB = nil

	pool, err := buildTelegramAgentDirectProviderPool(models.TelegramAgentConfig{
		BaseURL: "https://api.example.com/v1/chat/completions",
		APIKey:  "sk-test",
		Model:   "gpt-direct",
	}, true)
	if err != nil {
		t.Fatalf("加载直连 TG Agent 提供商失败: %v", err)
	}

	selected, ok := pool.Candidates[1]
	if !ok {
		t.Fatalf("期望直连候选提供商存在: %+v", pool.Candidates)
	}
	if pool.MaxRetry != 3 {
		t.Fatalf("直连最大尝试次数应为 3，实际为 %d", pool.MaxRetry)
	}
	if selected.ProviderName != "TG Agent 直连" || selected.ProviderModel != "gpt-direct" {
		t.Fatalf("直连提供商信息不正确: %+v", selected)
	}
	if selected.ProviderStyle != consts.StyleOpenAI || selected.responseStyle() != consts.StyleOpenAI {
		t.Fatalf("直连提供商应使用 OpenAI 兼容格式: %+v", selected)
	}

	var cfg map[string]string
	if err := json.Unmarshal([]byte(selected.ProviderConfig), &cfg); err != nil {
		t.Fatalf("解析直连提供商配置失败: %v", err)
	}
	if cfg["base_url"] != "https://api.example.com/v1" {
		t.Fatalf("直连 base_url 归一化不正确: %s", cfg["base_url"])
	}
	if cfg["api_key"] != "sk-test" {
		t.Fatalf("直连 api_key 不正确: %s", cfg["api_key"])
	}
}

func TestStreamTelegramAgentReplyRetriesDirectProvider(t *testing.T) {
	previousDB := models.DB
	defer func() {
		models.DB = previousDB
	}()
	models.DB = nil

	previousExecutor := telegramAgentProviderRequestExecutor
	defer func() {
		telegramAgentProviderRequestExecutor = previousExecutor
	}()

	attempts := 0
	telegramAgentProviderRequestExecutor = func(ctx context.Context, selected selectedModelProvider, body []byte, stream bool, startedAt time.Time) (*http.Response, int, error) {
		attempts++
		if selected.ProviderName != "TG Agent 直连" || selected.ProviderModel != "gpt-direct" {
			return nil, 0, fmt.Errorf("未预期的直连提供商: %+v", selected)
		}
		switch attempts {
		case 1:
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("error code: 502")),
			}, 5, nil
		case 2:
			streamBody := strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"重试成功"}}]}`,
				"",
				`data: [DONE]`,
				"",
			}, "\n")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(streamBody)),
			}, 8, nil
		default:
			return nil, 0, fmt.Errorf("未预期的重试次数: %d", attempts)
		}
	}

	var answer strings.Builder
	result, err := streamTelegramAgentReply(context.Background(), models.TelegramAgentConfig{
		BaseURL: "https://api.example.com/v1",
		APIKey:  "sk-test",
		Model:   "gpt-direct",
	}, nil, "测试重试", nil, 1, func(delta string) error {
		answer.WriteString(delta)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("TG Agent 直连重试后仍失败: %v", err)
	}
	if answer.String() != "重试成功" {
		t.Fatalf("期望收到重试成功回复，实际为 %q", answer.String())
	}
	if result.Selected.ProviderName != "TG Agent 直连" {
		t.Fatalf("期望使用直连提供商，实际为 %s", result.Selected.ProviderName)
	}
	if result.Retry != 1 || attempts != 2 {
		t.Fatalf("期望直连重试一次后成功，retry=%d attempts=%d", result.Retry, attempts)
	}
}

func TestStreamTelegramAgentReplyRetriesDirectProviderOnClientError(t *testing.T) {
	previousDB := models.DB
	defer func() {
		models.DB = previousDB
	}()
	models.DB = nil

	previousExecutor := telegramAgentProviderRequestExecutor
	defer func() {
		telegramAgentProviderRequestExecutor = previousExecutor
	}()

	attempts := 0
	telegramAgentProviderRequestExecutor = func(ctx context.Context, selected selectedModelProvider, body []byte, stream bool, startedAt time.Time) (*http.Response, int, error) {
		attempts++
		switch attempts {
		case 1:
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad request"}`)),
			}, 5, nil
		case 2:
			streamBody := strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"400 后重试成功"}}]}`,
				"",
				`data: [DONE]`,
				"",
			}, "\n")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(streamBody)),
			}, 8, nil
		default:
			return nil, 0, fmt.Errorf("未预期的重试次数: %d", attempts)
		}
	}

	var answer strings.Builder
	result, err := streamTelegramAgentReply(context.Background(), models.TelegramAgentConfig{
		BaseURL: "https://api.example.com/v1",
		APIKey:  "sk-test",
		Model:   "gpt-direct",
	}, nil, "测试 400 重试", nil, 1, func(delta string) error {
		answer.WriteString(delta)
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("TG Agent 直连 400 后应继续重试: %v", err)
	}
	if answer.String() != "400 后重试成功" {
		t.Fatalf("期望收到 400 后重试成功回复，实际为 %q", answer.String())
	}
	if result.Retry != 1 || attempts != 2 {
		t.Fatalf("期望 400 后重试一次成功，retry=%d attempts=%d", result.Retry, attempts)
	}
}

func TestTelegramSessionMessagesPersistedAndNewConversationIsolated(t *testing.T) {
	previousDB := models.DB
	defer func() {
		models.DB = previousDB
	}()

	db, err := gorm.Open(sqlite.Open("file:tg_agent_context_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(&models.TelegramAgentMessage{}, &models.TelegramAgentSession{}); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}

	ctx := context.Background()
	chatID := int64(6801293687)
	telegramSessions.Delete(chatID)
	t.Cleanup(func() {
		telegramSessions.Delete(chatID)
	})
	cfg := models.TelegramAgentConfig{MaxHistoryMessages: 3}
	allMessages := []chatMessage{
		{Role: "user", Content: "第一轮"},
		{Role: "assistant", Content: "回答一"},
		{Role: "user", Content: "第二轮"},
		{Role: "assistant", Content: "回答二"},
	}
	trimmed := trimTelegramHistory(allMessages, cfg)

	if err := saveTelegramSessionMessages(ctx, chatID, trimmed); err != nil {
		t.Fatalf("保存 TG 上下文失败: %v", err)
	}
	telegramSessions.Delete(chatID)

	loaded, err := loadTelegramSessionMessages(ctx, chatID, cfg)
	if err != nil {
		t.Fatalf("加载 TG 上下文失败: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("期望加载最近 3 条上下文，实际为 %d 条: %+v", len(loaded), loaded)
	}
	if loaded[0].Content != "回答一" || loaded[1].Content != "第二轮" || loaded[2].Content != "回答二" {
		t.Fatalf("上下文顺序不正确: %+v", loaded)
	}

	oldConversationID := getTelegramSession(chatID).conversationID
	newConversationID, err := startNewTelegramConversation(ctx, chatID)
	if err != nil {
		t.Fatalf("开启新 TG 会话失败: %v", err)
	}
	if strings.TrimSpace(newConversationID) == "" || newConversationID == oldConversationID {
		t.Fatalf("新会话 ID 不正确，旧=%q 新=%q", oldConversationID, newConversationID)
	}

	var count int64
	if err := db.Model(&models.TelegramAgentMessage{}).Where("chat_id = ?", chatID).Count(&count).Error; err != nil {
		t.Fatalf("统计 TG 上下文失败: %v", err)
	}
	if count != 3 {
		t.Fatalf("开启新会话不应删除旧上下文，实际剩余 %d 条", count)
	}

	loaded, err = loadTelegramSessionMessages(ctx, chatID, cfg)
	if err != nil {
		t.Fatalf("加载新 TG 会话上下文失败: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("新会话应为空上下文，实际为 %+v", loaded)
	}
}

func TestShouldBypassTelegramAgentOnlyForExplicitSystemStatus(t *testing.T) {
	if shouldBypassTelegramAgent("禁用claude的模型和开启deepseek的模型，然后检查gpt状态") {
		t.Fatalf("模型状态查询不应绕过 TG Agent")
	}
	if !shouldBypassTelegramAgent("系统状态") {
		t.Fatalf("明确系统状态查询应绕过 TG Agent")
	}
}

func TestTelegramAgentToolModelStatusExecutesDirectly(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_direct")
	ctx := context.Background()
	chatID := int64(6801293687)

	model := models.Model{Name: "gpt-tool-test", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	action, err := agenttools.BuildSetModelStatusActionWithMode(ctx, chatID, "gpt-tool-test", false, false)
	if err != nil {
		t.Fatalf("准备工具动作失败: %v", err)
	}
	text, err := agenttools.PrepareOrExecuteAction(ctx, telegramAgentToolRuntime(), action)
	if err != nil {
		t.Fatalf("执行工具动作失败: %v", err)
	}
	if !strings.Contains(text, "已禁用模型") {
		t.Fatalf("期望返回执行结果，实际为: %s", text)
	}
	assertTelegramToolTextHasNoVisibleID(t, text)

	var after models.Model
	if err := db.First(&after, model.ID).Error; err != nil {
		t.Fatalf("读取执行后的模型失败: %v", err)
	}
	if after.Status != 0 {
		t.Fatalf("应直接禁用模型，实际 status=%d", after.Status)
	}
}

func TestTelegramAgentToolBulkModelStatusByKeyword(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_bulk")
	ctx := context.Background()
	chatID := int64(6801293691)

	modelsToCreate := []models.Model{
		{Name: "claude-haiku-test", Status: 1},
		{Name: "claude-opus-test", Status: 1},
		{Name: "gpt-tool-keep", Status: 1},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	action, err := agenttools.BuildSetModelStatusActionWithMode(ctx, chatID, "claude", false, true)
	if err != nil {
		t.Fatalf("准备批量工具动作失败: %v", err)
	}
	text, err := agenttools.PrepareOrExecuteAction(ctx, telegramAgentToolRuntime(), action)
	if err != nil {
		t.Fatalf("执行批量工具动作失败: %v", err)
	}
	if !strings.Contains(text, "已禁用模型") || !strings.Contains(text, "数量：2 个") {
		t.Fatalf("期望返回批量执行结果，实际为: %s", text)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 1)
}

func TestTelegramAgentToolProviderStatusKeepsAssociationStatus(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_restore")
	ctx := context.Background()
	chatID := int64(6801293689)

	modelA := models.Model{Name: "model-a", Status: 1}
	modelB := models.Model{Name: "model-b", Status: 1}
	provider := models.Provider{Name: "ToolProvider", Status: 1}
	if err := db.Create(&modelA).Error; err != nil {
		t.Fatalf("创建模型 A 失败: %v", err)
	}
	if err := db.Create(&modelB).Error; err != nil {
		t.Fatalf("创建模型 B 失败: %v", err)
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	enabledAssoc := models.ModelWithProvider{ModelID: modelA.ID, ProviderID: provider.ID, ProviderModel: "model-a", Status: 1}
	disabledAssoc := models.ModelWithProvider{ModelID: modelB.ID, ProviderID: provider.ID, ProviderModel: "model-b", Status: 0}
	if err := db.Create(&enabledAssoc).Error; err != nil {
		t.Fatalf("创建启用关联失败: %v", err)
	}
	if err := db.Create(&disabledAssoc).Error; err != nil {
		t.Fatalf("创建禁用关联失败: %v", err)
	}

	disableAction, err := agenttools.BuildSetProviderStatusAction(ctx, chatID, "ToolProvider", false)
	if err != nil {
		t.Fatalf("准备关闭提供商动作失败: %v", err)
	}
	if _, err := agenttools.PrepareOrExecuteAction(ctx, telegramAgentToolRuntime(), disableAction); err != nil {
		t.Fatalf("关闭提供商失败: %v", err)
	}
	assertTelegramProviderStatus(t, db, provider.ID, 0)
	assertTelegramModelProviderStatus(t, db, enabledAssoc.ID, 1)
	assertTelegramModelProviderStatus(t, db, disabledAssoc.ID, 0)

	enableAction, err := agenttools.BuildSetProviderStatusAction(ctx, chatID, "ToolProvider", true)
	if err != nil {
		t.Fatalf("准备开启提供商动作失败: %v", err)
	}
	if _, err := agenttools.PrepareOrExecuteAction(ctx, telegramAgentToolRuntime(), enableAction); err != nil {
		t.Fatalf("开启提供商失败: %v", err)
	}
	assertTelegramProviderStatus(t, db, provider.ID, 1)
	assertTelegramModelProviderStatus(t, db, enabledAssoc.ID, 1)
	assertTelegramModelProviderStatus(t, db, disabledAssoc.ID, 0)
}

func TestTelegramAgentToolBulkModelStatusCleansRelatedExpression(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_related")
	ctx := context.Background()
	chatID := int64(6801293692)

	modelsToCreate := []models.Model{
		{Name: "claude-haiku-related", Status: 0},
		{Name: "claude-opus-related", Status: 0},
		{Name: "gpt-related-keep", Status: 0},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_related",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"claude的相关模型","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(toolResult, "已启用模型") || !strings.Contains(toolResult, "数量：2 个") {
		t.Fatalf("期望 function call 工具清理 claude 的相关表达，实际为: %s", toolResult)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 0)
}

func TestTelegramAgentFunctionToolExecutesModelStatusDirectly(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_call")
	ctx := context.Background()
	chatID := int64(6801293693)

	modelsToCreate := []models.Model{
		{Name: "claude-sonnet-llm", Status: 0},
		{Name: "claude-opus-llm", Status: 0},
		{Name: "gpt-llm-keep", Status: 0},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_bulk",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"claude","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(toolResult, "已启用模型") || !strings.Contains(toolResult, "数量：2 个") {
		t.Fatalf("期望 function call 直接执行模型操作，实际为: %s", toolResult)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 0)
}

func TestTelegramAgentFunctionToolMergesMultipleModelStatusActions(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_merge")
	ctx := context.Background()
	chatID := int64(6801293694)

	modelsToCreate := []models.Model{
		{Name: "claude-haiku-merge", Status: 0},
		{Name: "claude-opus-merge", Status: 0},
		{Name: "claude-sonnet-merge", Status: 0},
		{Name: "deepseek-flash-merge", Status: 0},
		{Name: "deepseek-pro-merge", Status: 0},
		{Name: "gpt-merge-keep", Status: 0},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	firstResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_claude",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"claude","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(firstResult, "已启用模型") || !strings.Contains(firstResult, "数量：3 个") {
		t.Fatalf("期望第一个工具调用启用 claude 模型，实际为: %s", firstResult)
	}

	secondResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_deepseek",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"deepseek","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(secondResult, "已启用模型") || !strings.Contains(secondResult, "数量：2 个") {
		t.Fatalf("期望第二个工具调用启用 deepseek 模型，实际为: %s", secondResult)
	}
	for index := 0; index < 5; index++ {
		assertTelegramModelStatus(t, db, modelsToCreate[index].ID, 1)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[5].ID, 0)
}

func TestTelegramAgentFunctionToolBatchesMixedModelStatusActions(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_mixed_batch")
	ctx := context.Background()
	chatID := int64(6801293699)

	modelsToCreate := []models.Model{
		{Name: "claude-haiku-mixed", Status: 1},
		{Name: "claude-opus-mixed", Status: 1},
		{Name: "deepseek-flash-mixed", Status: 0},
		{Name: "deepseek-pro-mixed", Status: 0},
		{Name: "gpt-mixed-keep", Status: 1},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	disableClaudeResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_disable_claude",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"claude","enabled":false,"bulk":true}`,
		},
	})
	if !strings.Contains(disableClaudeResult, "已禁用模型") || !strings.Contains(disableClaudeResult, "数量：2 个") {
		t.Fatalf("期望第一个工具调用禁用 claude，实际为: %s", disableClaudeResult)
	}

	enableDeepSeekResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_enable_deepseek",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"deepseek","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(enableDeepSeekResult, "已启用模型") || !strings.Contains(enableDeepSeekResult, "数量：2 个") {
		t.Fatalf("期望第二个工具调用启用 deepseek，实际为: %s", enableDeepSeekResult)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[3].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[4].ID, 1)
}

func TestTelegramAgentFunctionToolSetModelsStatusBatch(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_batch_tool")
	ctx := context.Background()
	chatID := int64(6801293700)

	modelsToCreate := []models.Model{
		{Name: "claude-haiku-batch-tool", Status: 1},
		{Name: "claude-opus-batch-tool", Status: 1},
		{Name: "deepseek-flash-batch-tool", Status: 0},
		{Name: "deepseek-pro-batch-tool", Status: 0},
		{Name: "gpt-batch-tool-keep", Status: 1},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_batch_model_status",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name: telegramAgentToolSetModelsStatusBatch,
			Arguments: `{
				"items":[
					{"target":"claude","enabled":false,"bulk":true},
					{"target":"deepseek","enabled":true,"bulk":true}
				]
			}`,
		},
	})
	if !strings.Contains(toolResult, "已执行批次操作") ||
		!strings.Contains(toolResult, "已禁用模型") ||
		!strings.Contains(toolResult, "已启用模型") {
		t.Fatalf("期望批量工具一次执行两个模型操作，实际为: %s", toolResult)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[3].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[4].ID, 1)
}

func TestTelegramAgentFunctionToolGetsProviderConfigMasked(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_get")
	ctx := context.Background()
	chatID := int64(6801293695)

	provider := models.Provider{
		Name:            "ProviderConfigMasked",
		Config:          `{"base_url":"https://api.example.com/v1","api_key":"sk-one,sk-two"}`,
		Console:         "https://console.example.com",
		ModelsFetchMode: "api_pricing",
		Capabilities:    models.ProviderCapabilities{"chat", "openai"},
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_get_provider_config",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolGetProviderConfig,
			Arguments: `{"target":"ProviderConfigMasked"}`,
		},
	})
	if !strings.Contains(toolResult, "api_key：已隐藏（2 个值）") {
		t.Fatalf("期望隐藏 api_key，实际为: %s", toolResult)
	}
	assertTelegramToolTextHasNoVisibleID(t, toolResult)
	if strings.Contains(toolResult, "sk-one") || strings.Contains(toolResult, "sk-two") {
		t.Fatalf("不应泄露 api_key 明文，实际为: %s", toolResult)
	}
}

func TestTelegramAgentFunctionToolUpdatesProviderConfigDirectly(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_update")
	ctx := context.Background()
	chatID := int64(6801293696)

	provider := models.Provider{
		Name:                       "ProviderConfigUpdate",
		Config:                     `{"base_url":"https://old.example.com/v1","api_key":"old-key","extra":"keep"}`,
		Console:                    "https://old.example.com",
		ProxyURL:                   "http://127.0.0.1:7890",
		ModelsFetchMode:            "v1_models",
		Capabilities:               models.ProviderCapabilities{"chat", "openai"},
		InterfaceConversionEnabled: 1,
		InterfaceConversionTarget:  "responses",
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_update_provider_config",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name: telegramAgentToolUpdateProviderConfig,
			Arguments: `{
				"target":"ProviderConfigUpdate",
				"config_updates":{"base_url":"https://new.example.com/v1","api_key":"new-a,new-b"},
				"remove_config_keys":["extra"],
				"console":"https://new-console.example.com",
				"proxy_url":"",
				"models_fetch_mode":"api_pricing",
				"capabilities":["chat","openai","claude"],
				"interface_conversion_enabled":false
			}`,
		},
	})
	if !strings.Contains(toolResult, "已更新提供商配置") {
		t.Fatalf("期望直接更新提供商配置，实际为: %s", toolResult)
	}
	assertTelegramToolTextHasNoVisibleID(t, toolResult)
	if strings.Contains(toolResult, "new-a") || strings.Contains(toolResult, "new-b") {
		t.Fatalf("工具返回不应泄露新 api_key，实际为: %s", toolResult)
	}

	var updated models.Provider
	if err := db.First(&updated, provider.ID).Error; err != nil {
		t.Fatalf("读取更新后提供商失败: %v", err)
	}
	config := map[string]string{}
	if err := json.Unmarshal([]byte(updated.Config), &config); err != nil {
		t.Fatalf("解析更新后 config 失败: %v", err)
	}
	if config["base_url"] != "https://new.example.com/v1" || config["api_key"] != "new-a,new-b" {
		t.Fatalf("config 未按预期更新: %#v", config)
	}
	if _, ok := config["extra"]; ok {
		t.Fatalf("extra 字段应已删除: %#v", config)
	}
	if updated.Console != "https://new-console.example.com" || updated.ProxyURL != "" || updated.ModelsFetchMode != "api_pricing" {
		t.Fatalf("提供商字段未按预期更新: %#v", updated)
	}
	if updated.InterfaceConversionEnabled != 0 || updated.InterfaceConversionTarget != "" {
		t.Fatalf("接口转换应关闭并清空目标: enabled=%d target=%s", updated.InterfaceConversionEnabled, updated.InterfaceConversionTarget)
	}
}

func TestTelegramAgentToolListOutputsHideIDs(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_list_hide_ids")
	ctx := context.Background()

	model := models.Model{Name: "list-output-model", Status: 1}
	provider := models.Provider{
		Name:   "ListOutputProvider",
		Config: `{"base_url":"https://list.example.com/v1","api_key":"sk-list-secret"}`,
	}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	modelText, err := agenttools.ListModels(ctx, "list-output")
	if err != nil {
		t.Fatalf("读取模型列表失败: %v", err)
	}
	if !strings.Contains(modelText, "启用｜list-output-model") {
		t.Fatalf("模型列表结构不符合预期: %s", modelText)
	}
	assertTelegramToolTextHasNoVisibleID(t, modelText)

	providerText, err := agenttools.ListProviders(ctx, "ListOutput")
	if err != nil {
		t.Fatalf("读取提供商列表失败: %v", err)
	}
	if !strings.Contains(providerText, "未关联｜ListOutputProvider｜URL https://list.example.com/v1｜API Key sk-list-secret｜启用关联 0/0") {
		t.Fatalf("提供商列表结构不符合预期: %s", providerText)
	}
	assertTelegramToolTextHasNoVisibleID(t, providerText)
}

func TestTelegramAgentFunctionToolUpdatesProviderConfigWithoutConfirm(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_update_direct")
	ctx := context.Background()
	chatID := int64(6801293698)

	provider := models.Provider{
		Name:   "DirectProviderConfigUpdate",
		Config: `{"base_url":"https://old.example.com/v1","api_key":"old-key"}`,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_direct_update_provider_config",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolUpdateProviderConfig,
			Arguments: `{"target":"DirectProviderConfigUpdate","config_updates":{"api_key":"11111"}}`,
		},
	})
	if !strings.Contains(toolResult, "已更新提供商配置") {
		t.Fatalf("期望直接执行，实际为: %s", toolResult)
	}

	var updated models.Provider
	if err := db.First(&updated, provider.ID).Error; err != nil {
		t.Fatalf("读取提供商失败: %v", err)
	}
	config := map[string]string{}
	if err := json.Unmarshal([]byte(updated.Config), &config); err != nil {
		t.Fatalf("解析提供商 config 失败: %v", err)
	}
	if config["api_key"] != "11111" {
		t.Fatalf("期望 api_key 直接更新为 11111，实际为: %#v", config)
	}
}

func TestTelegramAgentFunctionToolCreatesAuthKeyWithoutConfirm(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_auth_key_create")
	ctx := context.Background()

	modelsToCreate := []models.Model{
		{Name: "claude-auth-key-create", Status: 1},
		{Name: "gpt-auth-key-ignore", Status: 1},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, 6801293710, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_create_auth_key",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolCreateAuthKey,
			Arguments: `{"name":"TG 创建 Key","allow_all":false,"model_keywords":["claude-auth-key"],"rpm_limit":30}`,
		},
	})
	payload := agenttools.ParseResultPayload(toolResult)
	if !payload.OK || !strings.Contains(payload.Text, "已新增 API Key") {
		t.Fatalf("期望新增 API Key 成功，实际为: %s", toolResult)
	}
	if strings.Contains(payload.Text, "sk-github.com/racio/orvion-") {
		t.Fatalf("工具返回不应包含完整自动生成 Key，实际为: %s", payload.Text)
	}

	var authKey models.AuthKey
	if err := db.Where("name = ?", "TG 创建 Key").First(&authKey).Error; err != nil {
		t.Fatalf("读取新增 AuthKey 失败: %v", err)
	}
	if authKey.AllowAll != 0 || authKey.RpmLimit != 30 {
		t.Fatalf("AuthKey 权限或 RPM 不正确: %+v", authKey)
	}
	if !strings.Contains(authKey.Models, "claude-auth-key-create") || strings.Contains(authKey.Models, "gpt-auth-key-ignore") {
		t.Fatalf("AuthKey 模型列表不正确: %s", authKey.Models)
	}
}

func TestTelegramAgentFunctionToolUpdatesAuthKeyWithoutConfirm(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_auth_key_update")
	ctx := context.Background()
	expiresAt := time.Now().Add(24 * time.Hour)

	model := models.Model{Name: "deepseek-auth-key-update", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
	authKey := models.AuthKey{
		Name:      "TG 待修改 Key",
		Key:       "sk-auth-key-update",
		Status:    1,
		AllowAll:  1,
		Models:    "[]",
		ExpiresAt: &expiresAt,
		RpmLimit:  90,
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("创建 AuthKey 失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, 6801293711, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_update_auth_key",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolUpdateAuthKey,
			Arguments: `{"target":"TG 待修改 Key","enabled":false,"allow_all":false,"model_keywords":["deepseek-auth-key"],"clear_expires_at":true,"rpm_limit":0}`,
		},
	})
	payload := agenttools.ParseResultPayload(toolResult)
	if !payload.OK || !strings.Contains(payload.Text, "已更新 API Key") {
		t.Fatalf("期望修改 API Key 成功，实际为: %s", toolResult)
	}
	assertTelegramToolTextHasNoVisibleID(t, payload.Text)

	var updated models.AuthKey
	if err := db.First(&updated, authKey.ID).Error; err != nil {
		t.Fatalf("读取更新后 AuthKey 失败: %v", err)
	}
	if updated.Status != 0 || updated.AllowAll != 0 || updated.RpmLimit != 0 || updated.ExpiresAt != nil {
		t.Fatalf("AuthKey 零值字段未正确更新: %+v", updated)
	}
	if !strings.Contains(updated.Models, "deepseek-auth-key-update") {
		t.Fatalf("AuthKey 模型列表未正确更新: %s", updated.Models)
	}
}

func TestTelegramAgentFunctionToolListsAuthKeysMasked(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_auth_key_list")
	ctx := context.Background()
	lastUsedAt := time.Date(2026, 6, 4, 12, 30, 0, 0, time.Local)
	expiresAt := lastUsedAt.Add(24 * time.Hour)

	authKeys := []models.AuthKey{
		{
			Name:       "Alpha Key",
			Key:        "sk-alpha-secret-value",
			Status:     1,
			AllowAll:   1,
			Models:     "[]",
			RpmLimit:   60,
			UsageCount: 12,
			TotalCost:  1.25,
			LastUsedAt: &lastUsedAt,
			ExpiresAt:  &expiresAt,
		},
		{
			Name:      "Beta Key",
			Key:       "sk-beta-secret-value",
			Status:    0,
			AllowAll:  0,
			Models:    `["gpt-list-auth-key"]`,
			RpmLimit:  0,
			TotalCost: 0.5,
		},
	}
	if err := db.Create(&authKeys).Error; err != nil {
		t.Fatalf("创建 AuthKey 失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, 6801293712, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_list_auth_keys",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolListAuthKeys,
			Arguments: `{"status":"all","limit":10}`,
		},
	})
	payload := agenttools.ParseResultPayload(toolResult)
	if !payload.OK {
		t.Fatalf("期望查看 API Key 成功，实际为: %s", toolResult)
	}
	if !strings.Contains(payload.Text, "Alpha Key") || !strings.Contains(payload.Text, "Beta Key") {
		t.Fatalf("列表缺少预期项目：%s", payload.Text)
	}
	if strings.Contains(payload.Text, "sk-alpha-secret-value") || strings.Contains(payload.Text, "sk-beta-secret-value") {
		t.Fatalf("列表不应返回完整 Key：%s", payload.Text)
	}
	if strings.Contains(payload.Text, "ID") {
		t.Fatalf("列表不应返回数据库 ID：%s", payload.Text)
	}

	enabledOnly := executeTelegramAgentFunctionToolCall(ctx, 6801293712, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_list_enabled_auth_keys",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolListAuthKeys,
			Arguments: `{"status":"enabled","limit":10}`,
		},
	})
	enabledPayload := agenttools.ParseResultPayload(enabledOnly)
	if !enabledPayload.OK || !strings.Contains(enabledPayload.Text, "Alpha Key") || strings.Contains(enabledPayload.Text, "Beta Key") {
		t.Fatalf("启用筛选结果不正确：%s", enabledOnly)
	}
}

func TestTelegramAgentFunctionToolDirectExecutionContinuesToolLoop(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_direct_continue_loop")
	ctx := context.Background()
	chatID := int64(6801293703)

	model := models.Model{Name: "direct-loop-model", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolCalls := []telegramAgentOpenAIToolCall{
		{
			ID:   "call_direct_disable_model",
			Type: "function",
			Function: telegramAgentOpenAIFunctionCall{
				Name:      telegramAgentToolSetModelStatus,
				Arguments: `{"target":"direct-loop-model","enabled":false}`,
			},
		},
	}
	messages, directFinalText, err := appendTelegramAgentToolResults(ctx, chatID, models.TelegramAgentConfig{}, toolCalls, nil, nil)
	if err != nil {
		t.Fatalf("执行工具失败: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("期望写入 1 条 tool message，实际为 %d", len(messages))
	}
	if directFinalText != "" {
		t.Fatalf("直接执行不应提前结束工具循环，实际为: %s", directFinalText)
	}
	assertTelegramModelStatus(t, db, model.ID, 0)
}

func TestTelegramAgentToolCallsNeedResultSummary(t *testing.T) {
	if !telegramAgentToolCallsNeedResultSummary([]telegramAgentOpenAIToolCall{{
		Function: telegramAgentOpenAIFunctionCall{Name: telegramAgentToolRunTerminalCommand},
	}}) {
		t.Fatalf("run_terminal_command 执行完成后应进入结果整理状态")
	}
	if telegramAgentToolCallsNeedResultSummary([]telegramAgentOpenAIToolCall{{
		Function: telegramAgentOpenAIFunctionCall{Name: telegramAgentToolListModels},
	}}) {
		t.Fatalf("普通查询工具不应触发脚本结果整理状态")
	}
}

func TestTelegramAgentToolRunningStatus(t *testing.T) {
	searchText := telegramAgentToolRunningStatus(telegramAgentOpenAIToolCall{
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolListSkills,
			Arguments: `{"query":"广州天气"}`,
		},
	})
	if !strings.Contains(searchText, "正在查找 广州天气") {
		t.Fatalf("期望显示 Skill 查找状态，实际为: %s", searchText)
	}

	runText := telegramAgentToolRunningStatus(telegramAgentOpenAIToolCall{
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolRunTerminalCommand,
			Arguments: `{"skill":"ultimate-search","command":"bash","command_args":["dual-search.sh","--query","广州天气"]}`,
		},
	})
	if !strings.Contains(runText, "ultimate-search") || !strings.Contains(runText, "正在搜索 广州天气") {
		t.Fatalf("期望显示命令搜索状态，实际为: %s", runText)
	}

	commandText := telegramAgentToolRunningStatus(telegramAgentOpenAIToolCall{
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolRunTerminalCommand,
			Arguments: `{"command":"bash","command_args":["sync.sh"]}`,
		},
	})
	if !strings.Contains(commandText, "bash") || !strings.Contains(commandText, "正在运行") {
		t.Fatalf("期望显示命令运行状态，实际为: %s", commandText)
	}

	imageText := telegramAgentToolRunningStatus(telegramAgentOpenAIToolCall{
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolRunTerminalCommand,
			Arguments: `{"skill":"local-z-image-turbo","command":"python3","command_args":["scripts/generate.py","--query","一只小猫"]}`,
		},
	})
	if !strings.Contains(imageText, "local-z-image-turbo") || !strings.Contains(imageText, "正在生成图片 一只小猫") || strings.Contains(imageText, "正在搜索") {
		t.Fatalf("期望显示图片生成状态，实际为: %s", imageText)
	}

	imageNoPromptText := telegramAgentToolRunningStatus(telegramAgentOpenAIToolCall{
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolRunTerminalCommand,
			Arguments: `{"skill":"local-z-image-turbo","command":"python3","command_args":["scripts/generate.py"]}`,
		},
	})
	if imageNoPromptText != "local-z-image-turbo 正在生成图片..." {
		t.Fatalf("期望无提示词时只显示生成图片状态，实际为: %s", imageNoPromptText)
	}
}

func TestTelegramAgentEditableMessageContentStatusOverridesAnswer(t *testing.T) {
	content := telegramAgentEditableMessageContent("前置回答内容", "ultimate-search 正在搜索 广州天气...")
	if content != "ultimate-search 正在搜索 广州天气..." {
		t.Fatalf("工具状态应覆盖占位消息内容，实际为: %s", content)
	}
	if got := telegramAgentEditableMessageContent("最终回答", ""); got != "最终回答" {
		t.Fatalf("无状态时应显示回答内容，实际为: %s", got)
	}
	if got := telegramAgentEditableMessageContent("", ""); got != "正在思考..." {
		t.Fatalf("空内容时应显示默认占位，实际为: %s", got)
	}
}

func TestTelegramAgentFunctionToolProviderUpdateContinuesToolLoop(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_stop_loop")
	ctx := context.Background()
	chatID := int64(6801293697)

	provider := models.Provider{
		Name:   "小米公益",
		Config: `{"base_url":"https://old.example.com/v1","api_key":"old-key"}`,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	toolCalls := []telegramAgentOpenAIToolCall{
		{
			ID:   "call_update_provider_config",
			Type: "function",
			Function: telegramAgentOpenAIFunctionCall{
				Name:      telegramAgentToolUpdateProviderConfig,
				Arguments: `{"target":"小米公益","config_updates":{"api_key":"11111"}}`,
			},
		},
	}
	messages, directFinalText, err := appendTelegramAgentToolResults(ctx, chatID, models.TelegramAgentConfig{}, toolCalls, nil, nil)
	if err != nil {
		t.Fatalf("执行工具失败: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("期望写入 1 条 tool message，实际为 %d", len(messages))
	}
	if directFinalText != "" {
		t.Fatalf("普通工具执行结果不应直接结束工具循环，实际为: %s", directFinalText)
	}

	var updated models.Provider
	if err := db.First(&updated, provider.ID).Error; err != nil {
		t.Fatalf("读取提供商失败: %v", err)
	}
	if !strings.Contains(updated.Config, "11111") {
		t.Fatalf("应直接写入新 api_key: %s", updated.Config)
	}
}

func TestTelegramAgentToolCallLogMasksSensitiveArguments(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_call_log_mask")
	ctx := context.Background()
	chatID := int64(6801293702)

	provider := models.Provider{
		Name:   "SensitiveLogProvider",
		Config: `{"base_url":"https://old.example.com/v1","api_key":"old-key"}`,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_sensitive_update",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolUpdateProviderConfig,
			Arguments: `{"target":"SensitiveLogProvider","config_updates":{"api_key":"secret-new-key","base_url":"https://new.example.com/v1"}}`,
		},
	})
	if !strings.Contains(toolResult, "已更新提供商配置") {
		t.Fatalf("期望直接更新提供商配置，实际为: %s", toolResult)
	}

	var callLog models.TelegramAgentToolCallLog
	if err := db.Where("chat_id = ? AND source = ? AND tool_name = ?", chatID, agenttools.ToolLogSourceFunctionCall, telegramAgentToolUpdateProviderConfig).
		Order("id DESC").
		First(&callLog).Error; err != nil {
		t.Fatalf("读取工具调用日志失败: %v", err)
	}
	if callLog.Status != agenttools.ToolLogStatusExecuted {
		t.Fatalf("期望工具调用日志状态为已执行，实际为 %s", callLog.Status)
	}
	if strings.Contains(callLog.Arguments, "secret-new-key") {
		t.Fatalf("工具调用日志不应记录 api_key 明文: %s", callLog.Arguments)
	}
	if !strings.Contains(callLog.Arguments, "已隐藏") {
		t.Fatalf("工具调用日志应隐藏敏感字段，实际为: %s", callLog.Arguments)
	}
}

func TestTelegramAgentToolActionExecutingLogIsUpdated(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_action_executing_update")
	ctx := context.Background()
	action := agenttools.Action{
		ChatID:         6801293704,
		ConversationID: "tg-6801293704-test",
		Kind:           agenttools.ActionRunTerminalCommand,
		Summary:        "执行测试命令",
	}

	logID := agenttools.RecordToolActionExecutingLog(ctx, action, agenttools.ToolLogSourceToolAction)
	if logID == 0 {
		t.Fatalf("期望创建执行中日志")
	}

	var callLog models.TelegramAgentToolCallLog
	if err := db.First(&callLog, logID).Error; err != nil {
		t.Fatalf("读取执行中日志失败: %v", err)
	}
	if callLog.Status != agenttools.ToolLogStatusExecuting {
		t.Fatalf("期望状态为执行中，实际为 %s", callLog.Status)
	}

	agenttools.FinishPreparedActionLog(ctx, logID, action, strings.Join([]string{
		"已执行命令",
		"命令：python3 demo.py",
		"工作目录：/tmp/demo",
		"退出码：0",
		"stdout：",
		"hello",
		"stderr：",
		"warn",
	}, "\n"))
	if err := db.First(&callLog, logID).Error; err != nil {
		t.Fatalf("读取完成日志失败: %v", err)
	}
	if callLog.Status != agenttools.ToolLogStatusExecuted || callLog.Result != "stdout：\nhello\nstderr：\nwarn" || callLog.ExecutedAt == nil {
		t.Fatalf("执行中日志未被正确更新: %+v", callLog)
	}

	var count int64
	if err := db.Model(&models.TelegramAgentToolCallLog{}).Where("chat_id = ?", action.ChatID).Count(&count).Error; err != nil {
		t.Fatalf("统计工具日志失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("期望更新同一条日志，实际记录数为 %d", count)
	}
}

func TestTelegramAgentToolActionFailureLogUsesTerminalOutput(t *testing.T) {
	action := agenttools.Action{
		ChatID:  6801293705,
		Kind:    agenttools.ActionRunTerminalCommand,
		Summary: "执行失败命令",
	}
	log := agenttools.BuildToolActionFailureLog(action, errors.New(strings.Join([]string{
		"已执行命令",
		"命令：python3 fail.py",
		"工作目录：/tmp/demo",
		"退出码：1",
		"stdout：",
		"Submitting video generation task...",
		"stderr：",
		"Error submitting task: timeout",
	}, "\n")))

	expected := "stdout：\nSubmitting video generation task...\nstderr：\nError submitting task: timeout"
	if log.Result != expected || log.Error != expected {
		t.Fatalf("失败日志应只记录终端输出，实际为 result=%q error=%q", log.Result, log.Error)
	}
}

func TestTelegramAgentFunctionToolReadsSystemLogs(t *testing.T) {
	logPath := t.TempDir() + "/orvion-test.log"
	t.Setenv("LOG_FILE", logPath)
	if err := os.WriteFile(logPath, []byte(strings.Join([]string{
		`time=2026-06-04T10:00:00+08:00 level=INFO msg="系统启动"`,
		`time=2026-06-04T10:01:00+08:00 level=ERROR msg="请求失败" api_key=sk-secret timeout=true`,
	}, "\n")), 0o644); err != nil {
		t.Fatalf("写入测试系统日志失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(context.Background(), 6801293703, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_read_system_logs",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolReadSystemLogs,
			Arguments: `{"level":"error","query":"timeout","limit":5}`,
		},
	})
	payload := agenttools.ParseResultPayload(toolResult)
	if !payload.OK {
		t.Fatalf("读取系统日志失败: %s", payload.Text)
	}
	if !strings.Contains(payload.Text, "请求失败") || !strings.Contains(payload.Text, "timeout") {
		t.Fatalf("期望读取匹配的错误日志，实际为: %s", payload.Text)
	}
	if strings.Contains(payload.Text, "sk-secret") || !strings.Contains(payload.Text, "已隐藏") {
		t.Fatalf("系统日志返回应隐藏敏感值，实际为: %s", payload.Text)
	}
}

func TestTelegramAgentFunctionToolReadsRequestLogs(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_read_request_logs")
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 10, 20, 0, 0, time.Local)
	tableName := models.ChatLogMonthlyTableName(now)
	if err := db.Table(tableName).AutoMigrate(&models.ChatLog{}); err != nil {
		t.Fatalf("迁移日志月表失败: %v", err)
	}
	logs := []models.ChatLog{
		{
			CreatedAt:     now.Add(-time.Minute),
			Name:          "gpt-ok-log",
			ProviderName:  "OpenAI",
			ProviderModel: "gpt-upstream",
			Status:        "success",
			RequestPath:   "/v1/chat/completions",
		},
		{
			CreatedAt:     now,
			Name:          "claude-error-log",
			ProviderName:  "Anthropic",
			ProviderModel: "claude-upstream",
			Status:        "error",
			RequestPath:   "/v1/chat/completions",
			Error:         "http2: timeout awaiting response headers api_key=sk-request-secret",
			ProxyTimeMs:   3200,
			Usage: models.Usage{
				TotalTokens: 128,
				TotalCost:   0.0012,
			},
		},
	}
	if err := db.Table(tableName).Create(&logs).Error; err != nil {
		t.Fatalf("写入请求日志失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, 6801293704, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_read_request_logs",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolReadRequestLogs,
			Arguments: `{"status":"error","query":"timeout","limit":5}`,
		},
	})
	payload := agenttools.ParseResultPayload(toolResult)
	if !payload.OK {
		t.Fatalf("读取请求日志失败: %s", payload.Text)
	}
	if !strings.Contains(payload.Text, "claude-error-log") || !strings.Contains(payload.Text, "失败") {
		t.Fatalf("期望读取失败请求日志，实际为: %s", payload.Text)
	}
	if strings.Contains(payload.Text, "gpt-ok-log") {
		t.Fatalf("不应返回未匹配的成功请求日志，实际为: %s", payload.Text)
	}
	if strings.Contains(payload.Text, "sk-request-secret") || !strings.Contains(payload.Text, "已隐藏") {
		t.Fatalf("请求日志错误内容应隐藏敏感值，实际为: %s", payload.Text)
	}
	assertTelegramToolTextHasNoVisibleID(t, payload.Text)
}

func TestTelegramAgentSkillToolsListReadAndRun(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_skill_tools_run")
	ctx := context.Background()
	chatID := int64(6801293713)

	root := t.TempDir()
	demoDir := filepath.Join(root, "demo")
	scriptDir := filepath.Join(demoDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("创建 Skill 脚本目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(demoDir, "skills.md"), []byte(strings.Join([]string{
		"---",
		"name: demo",
		"description: Demo skill",
		"triggers: [demo, echo]",
		"scripts:",
		"  - name: echo",
		"    path: scripts/echo.sh",
		"    description: Echo args",
		"    timeout_ms: 5000",
		"---",
		"Demo instructions.",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("写入 Skill 文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "echo.sh"), []byte(strings.Join([]string{
		"#!/bin/sh",
		"cat >/dev/null",
		"printf '{\"ok\":true,\"text\":\"skill-ok\"}'",
	}, "\n")), 0o755); err != nil {
		t.Fatalf("写入 Skill 脚本失败: %v", err)
	}

	disabledDir := filepath.Join(root, "disabled")
	if err := os.MkdirAll(disabledDir, 0o755); err != nil {
		t.Fatalf("创建禁用 Skill 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(disabledDir, "SKILL.md"), []byte(strings.Join([]string{
		"---",
		"name: disabled",
		"description: Disabled skill",
		"---",
		"Disabled instructions.",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("写入禁用 Skill 文件失败: %v", err)
	}

	skillsEnabled := true
	cfg := models.TelegramAgentConfig{
		SkillsEnabled: &skillsEnabled,
		SkillsDir:     root,
	}
	if _, err := agenttools.SetTelegramAgentSkillEnabled(ctx, cfg, "disabled", false); err != nil {
		t.Fatalf("写入禁用 Skill 状态失败: %v", err)
	}

	listResult := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, telegramAgentOpenAIToolCall{
		ID:   "call_list_skills",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolListSkills,
			Arguments: `{"limit":10}`,
		},
	})
	listPayload := agenttools.ParseResultPayload(listResult)
	if !listPayload.OK {
		t.Fatalf("查看 Skills 失败: %s", listPayload.Text)
	}
	if !strings.Contains(listPayload.Text, "demo") || !strings.Contains(listPayload.Text, "disabled") || !strings.Contains(listPayload.Text, "禁用") {
		t.Fatalf("Skills 列表应包含启用和禁用状态，实际为: %s", listPayload.Text)
	}

	readResult := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, telegramAgentOpenAIToolCall{
		ID:   "call_read_skill",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolReadSkill,
			Arguments: `{"skill":"demo"}`,
		},
	})
	readPayload := agenttools.ParseResultPayload(readResult)
	if !readPayload.OK {
		t.Fatalf("读取 Skill 失败: %s", readPayload.Text)
	}
	if !strings.Contains(readPayload.Text, "Demo instructions.") || !strings.Contains(readPayload.Text, "echo") {
		t.Fatalf("Skill 详情应包含说明和脚本，实际为: %s", readPayload.Text)
	}

	commandArgs, err := json.Marshal(map[string]any{
		"command":      "bash",
		"command_args": []string{filepath.Join(scriptDir, "echo.sh")},
		"working_dir":  demoDir,
		"stdin":        `{"args":{"message":"hello"}}`,
	})
	if err != nil {
		t.Fatalf("构造命令参数失败: %v", err)
	}
	runResult := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, telegramAgentOpenAIToolCall{
		ID:   "call_run_skill",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolRunTerminalCommand,
			Arguments: string(commandArgs),
		},
	})
	runPayload := agenttools.ParseResultPayload(runResult)
	if !runPayload.OK || !runPayload.Final {
		t.Fatalf("执行 Skill 命令失败: %+v", runPayload)
	}
	if !strings.Contains(runPayload.Text, "skill-ok") || !strings.Contains(runPayload.Text, "已执行命令") || !strings.Contains(runPayload.Text, "Skill：demo") {
		t.Fatalf("Skill 命令输出不正确: %s", runPayload.Text)
	}

	var actionLog models.TelegramAgentToolCallLog
	if err := db.Where("chat_id = ? AND source = ? AND tool_name = ?", chatID, agenttools.ToolLogSourceToolAction, telegramAgentToolRunTerminalCommand).
		Order("id DESC").
		First(&actionLog).Error; err != nil {
		t.Fatalf("读取 Skill 命令审计日志失败: %v", err)
	}
	if actionLog.Status != agenttools.ToolLogStatusExecuted {
		t.Fatalf("Skill 命令审计日志状态不正确: %+v", actionLog)
	}
}

func TestTelegramAgentSkillScriptRunsDirectly(t *testing.T) {
	setupTelegramAgentToolTestDB(t, "tg_agent_skill_tools_direct")
	ctx := context.Background()
	chatID := int64(6801293714)

	root := t.TempDir()
	skillDir := filepath.Join(root, "direct")
	scriptDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("创建 Skill 脚本目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(strings.Join([]string{
		"---",
		"name: direct",
		"description: Direct skill",
		"scripts:",
		"  - name: dangerous",
		"    path: scripts/dangerous.sh",
		"    description: Runs directly",
		"---",
		"Direct instructions.",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("写入 Skill 文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "dangerous.sh"), []byte(strings.Join([]string{
		"#!/bin/sh",
		"printf '{\"ok\":true,\"text\":\"direct-run\"}'",
	}, "\n")), 0o755); err != nil {
		t.Fatalf("写入 Skill 脚本失败: %v", err)
	}

	skillsEnabled := true
	cfg := models.TelegramAgentConfig{
		SkillsEnabled: &skillsEnabled,
		SkillsDir:     root,
	}
	commandArgs, err := json.Marshal(map[string]any{
		"command":      "bash",
		"command_args": []string{filepath.Join(scriptDir, "dangerous.sh")},
		"working_dir":  skillDir,
	})
	if err != nil {
		t.Fatalf("构造命令参数失败: %v", err)
	}
	result := executeTelegramAgentFunctionToolCall(ctx, chatID, cfg, telegramAgentOpenAIToolCall{
		ID:   "call_run_skill_direct",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolRunTerminalCommand,
			Arguments: string(commandArgs),
		},
	})
	payload := agenttools.ParseResultPayload(result)
	if !payload.OK || !payload.Final || !strings.Contains(payload.Text, "direct-run") {
		t.Fatalf("Skill 脚本应直接执行，实际为: %+v", payload)
	}
}

func TestTelegramAgentSkillMarkdownUsageCommandIsRecognized(t *testing.T) {
	setupTelegramAgentToolTestDB(t, "tg_agent_skill_markdown_usage")
	ctx := context.Background()

	root := t.TempDir()
	skillDir := filepath.Join(root, "video-generation")
	scriptDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("创建 Skill 脚本目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "generate_video.py"), []byte("print('ok')\n"), 0o755); err != nil {
		t.Fatalf("写入视频脚本失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(strings.Join([]string{
		"# Video Generation Skill",
		"",
		"## Description",
		"This skill generates videos using the agnes-video-v2.0 model via HTTP API.",
		"It executes a local Python script that internally uses system curl.",
		"",
		"## Capabilities",
		"- Text-to-Video generation",
		"- Image-to-Video generation",
		"",
		"## Usage",
		"When the user requests video generation, execute:",
		"",
		"```bash",
		"python3 scripts/generate_video.py --mode txt2vid --prompt \"<prompt>\"",
		"```",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("写入 Skill 文件失败: %v", err)
	}

	skillsEnabled := true
	cfg := models.TelegramAgentConfig{
		SkillsEnabled: &skillsEnabled,
		SkillsDir:     root,
	}

	skill, err := agenttools.ParseTelegramAgentSkillFromDir(skillDir)
	if err != nil {
		t.Fatalf("解析 Skill 失败: %v", err)
	}
	if !strings.Contains(skill.Description, "generates videos") {
		t.Fatalf("应从 Markdown Description 自动提取描述，实际为: %q", skill.Description)
	}
	if len(skill.Scripts) != 1 || skill.Scripts[0].Name != "generate_video" {
		t.Fatalf("应识别 generate_video 脚本，实际为: %+v", skill.Scripts)
	}
	if len(skill.Scripts[0].Usage) != 1 || !strings.Contains(skill.Scripts[0].Usage[0], "--mode txt2vid") {
		t.Fatalf("应从 Usage 代码块提取命令模板，实际为: %+v", skill.Scripts[0].Usage)
	}

	detail, err := agenttools.ReadTelegramAgentSkill(ctx, cfg, telegramAgentToolCallArgs{Skill: "video-generation"})
	if err != nil {
		t.Fatalf("读取 Skill 失败: %v", err)
	}
	if !strings.Contains(detail, "推荐命令模板") || !strings.Contains(detail, "python3 scripts/generate_video.py --mode txt2vid --prompt \"<prompt>\"") {
		t.Fatalf("Skill 详情应展示推荐命令模板，实际为: %s", detail)
	}
}

func TestTelegramAgentSkillManagementListToggleAndImport(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_skill_management")
	ctx := context.Background()
	root := t.TempDir()
	sourceRoot := t.TempDir()

	writeTelegramAgentTestSkill(t, root, "deploy", "skills.md", "Deploy project pipeline", "run")
	sourceSkillDir := writeTelegramAgentTestSkill(t, sourceRoot, "docs", "SKILL.md", "Generate project docs", "build")

	skillsEnabled := true
	cfg := models.TelegramAgentConfig{
		SkillsEnabled: &skillsEnabled,
		SkillsDir:     root,
	}

	listResult, err := agenttools.ListTelegramAgentSkillsForManagement(ctx, cfg, "deploy")
	if err != nil {
		t.Fatalf("关键词检索 Skill 失败: %v", err)
	}
	if len(listResult.Skills) != 1 || listResult.Skills[0].Name != "deploy" {
		t.Fatalf("期望检索到 deploy Skill，实际为: %+v", listResult.Skills)
	}

	updated, err := agenttools.SetTelegramAgentSkillEnabled(ctx, cfg, "deploy", false)
	if err != nil {
		t.Fatalf("禁用 Skill 失败: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("期望 Skill 已禁用: %+v", updated)
	}
	var skillRecord models.TelegramAgentSkill
	if err := db.Where("name = ?", "deploy").First(&skillRecord).Error; err != nil {
		t.Fatalf("应写入 Skill 状态表: %v", err)
	}
	if skillRecord.Enabled != 0 {
		t.Fatalf("Skill 状态表应记录禁用，实际为: %+v", skillRecord)
	}
	raw, err := os.ReadFile(filepath.Join(root, "deploy", "skills.md"))
	if err != nil {
		t.Fatalf("读取 Skill 文件失败: %v", err)
	}
	if strings.Contains(string(raw), "enabled: false") {
		t.Fatalf("Skill 文件不应写入禁用状态: %s", raw)
	}

	imported, err := agenttools.ImportTelegramAgentSkill(ctx, cfg, agenttools.TelegramAgentSkillImportRequest{
		SourcePath: sourceSkillDir,
	})
	if err != nil {
		t.Fatalf("导入 Skill 失败: %v", err)
	}
	if imported.Name != "docs" {
		t.Fatalf("导入后的 Skill 名称不正确: %+v", imported)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "SKILL.md")); err != nil {
		t.Fatalf("导入后的 Skill 文件不存在: %v", err)
	}

	mismatchDir := writeTelegramAgentTestSkillWithMetaName(t, sourceRoot, "UltimateSearchSkill-main", "ultimate-search", "SKILL.md", "Search skill", "search")
	importedMismatch, err := agenttools.ImportTelegramAgentSkill(ctx, cfg, agenttools.TelegramAgentSkillImportRequest{
		SourcePath: mismatchDir,
	})
	if err != nil {
		t.Fatalf("导入目录名和 Skill 名不同的 Skill 失败: %v", err)
	}
	if importedMismatch.Name != "ultimate-search" {
		t.Fatalf("应返回 SKILL.md 中的真实名称，实际为: %+v", importedMismatch)
	}
	if _, err := os.Stat(filepath.Join(root, "UltimateSearchSkill-main", "SKILL.md")); err != nil {
		t.Fatalf("目录名不同的 Skill 文件未复制成功: %v", err)
	}

	importedExisting, err := agenttools.ImportTelegramAgentSkill(ctx, cfg, agenttools.TelegramAgentSkillImportRequest{
		SourcePath: mismatchDir,
	})
	if err != nil {
		t.Fatalf("重复导入已存在且有效的 Skill 应返回已有记录: %v", err)
	}
	if importedExisting.Name != "ultimate-search" {
		t.Fatalf("重复导入应返回已有 Skill 的真实名称，实际为: %+v", importedExisting)
	}
}

func TestTelegramAgentSkillFunctionDefinitionsExposeAllEnabledSkills(t *testing.T) {
	ctx := context.Background()
	setupTelegramAgentToolTestDB(t, "tg_agent_skill_enabled_definitions")
	root := t.TempDir()
	writeTelegramAgentTestSkill(t, root, "writer", "SKILL.md", "Write release notes", "compose")
	writeTelegramAgentTestSkill(t, root, "weather", "SKILL.md", "Weather forecast rainfall temperature", "forecast")

	skillsEnabled := true
	disabled := false
	withoutSkills := telegramAgentFunctionToolDefinitions(ctx, models.TelegramAgentConfig{
		SkillsEnabled: &disabled,
		SkillsDir:     root,
	})
	if _, ok := findTelegramAgentFunctionDefinitionForTest(withoutSkills, telegramAgentToolListSkills); ok {
		t.Fatalf("Skills 未启用时不应暴露 Skills 工具")
	}

	cfg := models.TelegramAgentConfig{
		SkillsEnabled: &skillsEnabled,
		SkillsDir:     root,
	}
	if _, err := agenttools.SetTelegramAgentSkillEnabled(ctx, cfg, "weather", false); err != nil {
		t.Fatalf("禁用非目标 Skill 失败: %v", err)
	}

	withSkills := telegramAgentFunctionToolDefinitions(ctx, cfg)
	if _, ok := findTelegramAgentFunctionDefinitionForTest(withSkills, telegramAgentToolListSkills); !ok {
		t.Fatalf("Skills 启用时应暴露 list_skills")
	}
	readDef, ok := findTelegramAgentFunctionDefinitionForTest(withSkills, telegramAgentToolReadSkill)
	if !ok {
		t.Fatalf("Skills 启用且存在启用项时应暴露 read_skill")
	}
	if _, ok := findTelegramAgentFunctionDefinitionForTest(withSkills, telegramAgentToolRunTerminalCommand); !ok {
		t.Fatalf("Skills 启用且存在启用项时应暴露 run_terminal_command")
	}
	skillProp, ok := readDef.Properties["skill"].(map[string]any)
	if !ok {
		t.Fatalf("read_skill 缺少 skill 属性: %+v", readDef.Properties)
	}
	enumValues, ok := skillProp["enum"].([]string)
	if !ok || len(enumValues) != 1 || enumValues[0] != "writer" {
		t.Fatalf("Skill enum 未按启用状态生成: %+v", skillProp["enum"])
	}
	if strings.Contains(readDef.Description, "Write release notes") || strings.Contains(readDef.Description, "Weather forecast") {
		t.Fatalf("工具声明不应携带具体 Skill 元数据，实际为: %s", readDef.Description)
	}

	systemPrompt := telegramAgentFunctionToolSystemPrompt(ctx, cfg)
	if !strings.Contains(systemPrompt, "name: writer") || !strings.Contains(systemPrompt, "description: Write release notes") {
		t.Fatalf("系统提示应注入启用 Skill 元数据，实际为: %s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "name: weather") || strings.Contains(systemPrompt, "Weather forecast") {
		t.Fatalf("系统提示不应注入禁用 Skill 元数据，实际为: %s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "compose.sh") || strings.Contains(systemPrompt, "Skill instructions") {
		t.Fatalf("系统提示第一阶段不应注入脚本路径或 Body，实际为: %s", systemPrompt)
	}

	enabledSkills, err := agenttools.LoadTelegramAgentEnabledSkills(ctx, cfg)
	if err != nil {
		t.Fatalf("加载启用 Skill 失败: %v", err)
	}
	if len(enabledSkills) != 1 || enabledSkills[0].Name != "writer" {
		t.Fatalf("应只返回启用 Skill，实际为: %+v", enabledSkills)
	}
}

func TestTelegramAgentSystemManagementToolsRegisteredAndInvokeHook(t *testing.T) {
	ctx := context.Background()
	definitions := telegramAgentFunctionToolDefinitions(ctx, models.TelegramAgentConfig{})
	for _, name := range []string{
		telegramAgentToolGetSystemStatus,
		telegramAgentToolGetPerformanceStats,
		telegramAgentToolListImageCache,
		telegramAgentToolDeleteImageCache,
		telegramAgentToolRefreshImageCache,
		telegramAgentToolGetBackgroundTasks,
		telegramAgentToolTriggerBackgroundTask,
	} {
		if _, ok := findTelegramAgentFunctionDefinitionForTest(definitions, name); !ok {
			t.Fatalf("系统管理工具未注册: %s", name)
		}
	}

	called := false
	agenttools.SetTelegramAgentSystemToolHooks(agenttools.TelegramAgentSystemToolHooks{
		GetSystemStatus: func(_ context.Context, req agenttools.TelegramAgentSystemToolRequest) (string, error) {
			called = true
			if req.Query != "状态" {
				t.Fatalf("hook 参数传递异常: %+v", req)
			}
			return "系统状态正常", nil
		},
	})
	t.Cleanup(func() {
		agenttools.SetTelegramAgentSystemToolHooks(agenttools.TelegramAgentSystemToolHooks{})
	})

	raw := agenttools.ExecuteFunctionTool(ctx, telegramAgentToolRuntime(), 0, models.TelegramAgentConfig{}, telegramAgentToolGetSystemStatus, telegramAgentToolCallArgs{Query: "状态"})
	payload := agenttools.ParseResultPayload(raw)
	if !called || !payload.OK || payload.Text != "系统状态正常" {
		t.Fatalf("系统管理 hook 调用失败 called=%v payload=%+v raw=%s", called, payload, raw)
	}
}

func TestTelegramAgentFunctionToolRequiredIsArray(t *testing.T) {
	tool := telegramAgentFunctionTool(
		telegramAgentToolListModels,
		"查看模型",
		map[string]any{"query": map[string]any{"type": "string"}},
		nil,
	)
	function, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatalf("工具声明缺少 function: %+v", tool)
	}
	parameters, ok := function["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("工具声明缺少 parameters: %+v", function)
	}
	required, ok := parameters["required"].([]string)
	if !ok {
		t.Fatalf("required 必须是数组，实际为 %T: %+v", parameters["required"], parameters["required"])
	}
	if len(required) != 0 {
		t.Fatalf("无必填参数时 required 应为空数组，实际为 %+v", required)
	}
}

func TestMergeTelegramAgentConfigIgnoresStoredSkillsDir(t *testing.T) {
	merged := mergeTelegramAgentConfig(
		models.TelegramAgentConfig{SkillsDir: "env-skills"},
		models.TelegramAgentConfig{SkillsDir: "db-skills"},
	)
	if merged.SkillsDir != "env-skills" {
		t.Fatalf("skills_dir 应只由环境变量或默认值控制，实际为: %s", merged.SkillsDir)
	}
}

func writeTelegramAgentTestSkill(t *testing.T, root string, name string, fileName string, description string, scriptName string) string {
	return writeTelegramAgentTestSkillWithMetaName(t, root, name, name, fileName, description, scriptName)
}

func writeTelegramAgentTestSkillWithMetaName(t *testing.T, root string, dirName string, metaName string, fileName string, description string, scriptName string) string {
	t.Helper()
	dir := filepath.Join(root, dirName)
	scriptDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("创建测试 Skill 目录失败: %v", err)
	}
	lines := []string{
		"---",
		"name: " + metaName,
		"description: " + description,
		"triggers: [" + metaName + "]",
		"scripts:",
		"  - name: " + scriptName,
		"    path: scripts/" + scriptName + ".sh",
		"    description: " + description,
		"---",
		description,
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("写入测试 Skill 文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, scriptName+".sh"), []byte("#!/bin/sh\nprintf '{\"ok\":true,\"text\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("写入测试 Skill 脚本失败: %v", err)
	}
	return dir
}

func findTelegramAgentFunctionDefinitionForTest(definitions []telegramAgentFunctionToolDefinition, name string) (telegramAgentFunctionToolDefinition, bool) {
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, true
		}
	}
	return telegramAgentFunctionToolDefinition{}, false
}

func setupTelegramAgentToolTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	previousDB := models.DB
	t.Cleanup(func() {
		models.DB = previousDB
	})

	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(
		&models.Config{},
		&models.Model{},
		&models.Provider{},
		&models.ModelWithProvider{},
		&models.AuthKey{},
		&models.TelegramAgentMessage{},
		&models.TelegramAgentSession{},
		&models.TelegramAgentToolCallLog{},
		&models.TelegramAgentScheduledTask{},
		&models.TelegramAgentSkill{},
	); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}
	return db
}

func assertTelegramModelProviderStatus(t *testing.T, db *gorm.DB, id uint, expected int) {
	t.Helper()
	var association models.ModelWithProvider
	if err := db.First(&association, id).Error; err != nil {
		t.Fatalf("读取模型提供商关联失败: %v", err)
	}
	if association.Status != expected {
		t.Fatalf("关联 ID %d 期望 status=%d，实际 status=%d", id, expected, association.Status)
	}
}

func assertTelegramProviderStatus(t *testing.T, db *gorm.DB, id uint, expected int) {
	t.Helper()
	var provider models.Provider
	if err := db.First(&provider, id).Error; err != nil {
		t.Fatalf("读取提供商失败: %v", err)
	}
	if provider.Status != expected {
		t.Fatalf("提供商 ID %d 期望 status=%d，实际 status=%d", id, expected, provider.Status)
	}
}

func assertTelegramModelStatus(t *testing.T, db *gorm.DB, id uint, expected int) {
	t.Helper()
	var model models.Model
	if err := db.First(&model, id).Error; err != nil {
		t.Fatalf("读取模型失败: %v", err)
	}
	if model.Status != expected {
		t.Fatalf("模型 ID %d 期望 status=%d，实际 status=%d", id, expected, model.Status)
	}
}

func assertTelegramToolTextHasNoVisibleID(t *testing.T, text string) {
	t.Helper()
	for _, marker := range []string{"ID ", "ID：", "(ID", "（ID"} {
		if strings.Contains(text, marker) {
			t.Fatalf("工具返回内容不应包含 ID 标记 %q，实际为: %s", marker, text)
		}
	}
}
