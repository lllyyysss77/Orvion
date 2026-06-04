package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
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

func TestTelegramSessionMessagesPersistedAndCleared(t *testing.T) {
	previousDB := models.DB
	defer func() {
		models.DB = previousDB
	}()

	db, err := gorm.Open(sqlite.Open("file:tg_agent_context_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(&models.TelegramAgentMessage{}); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}

	ctx := context.Background()
	chatID := int64(6801293687)
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

	if err := clearTelegramSession(ctx, chatID); err != nil {
		t.Fatalf("清理 TG 上下文失败: %v", err)
	}
	var count int64
	if err := db.Model(&models.TelegramAgentMessage{}).Where("chat_id = ?", chatID).Count(&count).Error; err != nil {
		t.Fatalf("统计 TG 上下文失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("期望上下文被清空，仍剩余 %d 条", count)
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

func TestTelegramAgentToolModelStatusRequiresConfirmation(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_confirm")
	ctx := context.Background()
	chatID := int64(6801293687)
	telegramPendingToolActions.Delete(chatID)

	model := models.Model{Name: "gpt-tool-test", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "/disable_model gpt-tool-test"})
	if err != nil || !handled {
		t.Fatalf("期望工具消息被处理，handled=%v err=%v", handled, err)
	}
	if !strings.Contains(client.lastSent(), "操作：禁用模型") {
		t.Fatalf("期望返回待确认提示，实际为: %s", client.lastSent())
	}
	assertTelegramToolTextHasNoVisibleID(t, client.lastSent())

	var before models.Model
	if err := db.First(&before, model.ID).Error; err != nil {
		t.Fatalf("读取模型失败: %v", err)
	}
	if before.Status != 1 {
		t.Fatalf("确认前不应修改模型状态，实际 status=%d", before.Status)
	}

	handled, err = HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认消息被处理，handled=%v err=%v", handled, err)
	}
	var after models.Model
	if err := db.First(&after, model.ID).Error; err != nil {
		t.Fatalf("读取确认后的模型失败: %v", err)
	}
	if after.Status != 0 {
		t.Fatalf("确认后应禁用模型，实际 status=%d", after.Status)
	}
	if !strings.Contains(client.lastSent(), "已禁用模型") {
		t.Fatalf("期望返回执行结果，实际为: %s", client.lastSent())
	}
	assertTelegramToolTextHasNoVisibleID(t, client.lastSent())
}

func TestTelegramAgentToolCancelKeepsModelStatus(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_cancel")
	ctx := context.Background()
	chatID := int64(6801293688)
	telegramPendingToolActions.Delete(chatID)

	model := models.Model{Name: "gpt-tool-cancel", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	client := &telegramToolTestClient{}
	if handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "/disable_model gpt-tool-cancel"}); err != nil || !handled {
		t.Fatalf("期望工具消息被处理，handled=%v err=%v", handled, err)
	}
	if handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "取消"}); err != nil || !handled {
		t.Fatalf("期望取消消息被处理，handled=%v err=%v", handled, err)
	}

	var after models.Model
	if err := db.First(&after, model.ID).Error; err != nil {
		t.Fatalf("读取模型失败: %v", err)
	}
	if after.Status != 1 {
		t.Fatalf("取消后不应修改模型状态，实际 status=%d", after.Status)
	}
	if !strings.Contains(client.lastSent(), "已取消") {
		t.Fatalf("期望返回取消提示，实际为: %s", client.lastSent())
	}
}

func TestTelegramAgentToolBulkModelStatusByKeyword(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_bulk")
	ctx := context.Background()
	chatID := int64(6801293691)
	telegramPendingToolActions.Delete(chatID)

	modelsToCreate := []models.Model{
		{Name: "claude-haiku-test", Status: 1},
		{Name: "claude-opus-test", Status: 1},
		{Name: "gpt-tool-keep", Status: 1},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "/disable_model claude的所有模型"})
	if err != nil || !handled {
		t.Fatalf("期望批量工具消息被处理，handled=%v err=%v", handled, err)
	}
	if !strings.Contains(client.lastSent(), "操作：禁用模型（匹配“claude”，共 2 个）") {
		t.Fatalf("期望返回批量待确认提示，实际为: %s", client.lastSent())
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 1)

	handled, err = HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认批量操作被处理，handled=%v err=%v", handled, err)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 1)
	if !strings.Contains(client.lastSent(), "数量：2 个") {
		t.Fatalf("期望返回批量执行结果，实际为: %s", client.lastSent())
	}
}

func TestTelegramAgentToolLooseConfirmWithoutPendingFallsThrough(t *testing.T) {
	chatID := int64(6801293690)
	telegramPendingToolActions.Delete(chatID)

	client := &telegramToolTestClient{}
	handled, err := handleTelegramAgentToolMessage(context.Background(), client, chatID, "好", models.TelegramAgentConfig{})
	if err != nil {
		t.Fatalf("处理宽泛确认词失败: %v", err)
	}
	if handled {
		t.Fatalf("没有待确认操作时，宽泛确认词不应被工具层截获")
	}
}

func TestTelegramAgentToolProviderStatusRestoresSnapshotOnly(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_restore")
	ctx := context.Background()
	chatID := int64(6801293689)
	telegramPendingToolActions.Delete(chatID)

	modelA := models.Model{Name: "model-a", Status: 1}
	modelB := models.Model{Name: "model-b", Status: 1}
	provider := models.Provider{Name: "ToolProvider"}
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

	client := &telegramToolTestClient{}
	if handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "/disable_provider ToolProvider"}); err != nil || !handled {
		t.Fatalf("期望关闭提供商消息被处理，handled=%v err=%v", handled, err)
	}
	assertTelegramModelProviderStatus(t, db, enabledAssoc.ID, 1)
	assertTelegramModelProviderStatus(t, db, disabledAssoc.ID, 0)

	if handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"}); err != nil || !handled {
		t.Fatalf("期望确认关闭被处理，handled=%v err=%v", handled, err)
	}
	assertTelegramModelProviderStatus(t, db, enabledAssoc.ID, 0)
	assertTelegramModelProviderStatus(t, db, disabledAssoc.ID, 0)

	if handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "/enable_provider ToolProvider"}); err != nil || !handled {
		t.Fatalf("期望开启提供商消息被处理，handled=%v err=%v", handled, err)
	}
	if handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"}); err != nil || !handled {
		t.Fatalf("期望确认开启被处理，handled=%v err=%v", handled, err)
	}
	assertTelegramModelProviderStatus(t, db, enabledAssoc.ID, 1)
	assertTelegramModelProviderStatus(t, db, disabledAssoc.ID, 0)

	var snapshotCount int64
	if err := db.Model(&models.Config{}).Where("key = ?", telegramProviderStatusSnapshotKey(provider.ID)).Count(&snapshotCount).Error; err != nil {
		t.Fatalf("统计快照失败: %v", err)
	}
	if snapshotCount != 0 {
		t.Fatalf("恢复后应删除提供商快照，实际剩余 %d 条", snapshotCount)
	}
}

func TestTelegramAgentToolBulkModelStatusCleansRelatedExpression(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_related")
	ctx := context.Background()
	chatID := int64(6801293692)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(toolResult, "操作：启用模型（匹配“claude”，共 2 个）") {
		t.Fatalf("期望 function call 工具清理 claude 的相关表达，实际为: %s", toolResult)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认批量启用被处理，handled=%v err=%v", handled, err)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 0)
}

func TestTelegramAgentFunctionToolCreatesPendingAction(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_call")
	ctx := context.Background()
	chatID := int64(6801293693)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(toolResult, "操作：启用模型（匹配“claude”，共 2 个）") {
		t.Fatalf("期望 function call 创建待确认操作，实际为: %s", toolResult)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 0)
}

func TestTelegramAgentFunctionToolMergesMultipleModelStatusActions(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_merge")
	ctx := context.Background()
	chatID := int64(6801293694)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(firstResult, "操作：启用模型（匹配“claude”，共 3 个）") {
		t.Fatalf("期望第一个工具调用准备 claude 模型，实际为: %s", firstResult)
	}

	secondResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_deepseek",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"deepseek","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(secondResult, "操作：启用模型（共 5 个）") {
		t.Fatalf("期望第二个工具调用合并为 5 个模型，实际为: %s", secondResult)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认合并操作被处理，handled=%v err=%v", handled, err)
	}
	for index := 0; index < 5; index++ {
		assertTelegramModelStatus(t, db, modelsToCreate[index].ID, 1)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[5].ID, 0)
	if !strings.Contains(client.lastSent(), "数量：5 个") {
		t.Fatalf("期望确认后启用 5 个模型，实际为: %s", client.lastSent())
	}
}

func TestTelegramAgentFunctionToolBatchesMixedModelStatusActions(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_mixed_batch")
	ctx := context.Background()
	chatID := int64(6801293699)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(disableClaudeResult, "操作：禁用模型（匹配“claude”，共 2 个）") {
		t.Fatalf("期望第一个工具调用准备禁用 claude，实际为: %s", disableClaudeResult)
	}

	enableDeepSeekResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_enable_deepseek",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"deepseek","enabled":true,"bulk":true}`,
		},
	})
	if !strings.Contains(enableDeepSeekResult, "批次操作（2 项）") ||
		!strings.Contains(enableDeepSeekResult, "禁用模型（匹配“claude”，共 2 个）") ||
		!strings.Contains(enableDeepSeekResult, "启用模型（匹配“deepseek”，共 2 个）") {
		t.Fatalf("期望混合操作合并为批次，实际为: %s", enableDeepSeekResult)
	}

	for _, model := range modelsToCreate {
		assertTelegramModelStatus(t, db, model.ID, model.Status)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认混合批次被处理，handled=%v err=%v", handled, err)
	}
	assertTelegramModelStatus(t, db, modelsToCreate[0].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[1].ID, 0)
	assertTelegramModelStatus(t, db, modelsToCreate[2].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[3].ID, 1)
	assertTelegramModelStatus(t, db, modelsToCreate[4].ID, 1)
	if !strings.Contains(client.lastSent(), "已执行批次操作") ||
		!strings.Contains(client.lastSent(), "已禁用模型") ||
		!strings.Contains(client.lastSent(), "已启用模型") {
		t.Fatalf("期望返回批次执行结果，实际为: %s", client.lastSent())
	}
}

func TestTelegramAgentFunctionToolSetModelsStatusBatch(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_model_function_batch_tool")
	ctx := context.Background()
	chatID := int64(6801293700)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(toolResult, "批次操作（2 项）") ||
		!strings.Contains(toolResult, "禁用模型（匹配“claude”，共 2 个）") ||
		!strings.Contains(toolResult, "启用模型（匹配“deepseek”，共 2 个）") {
		t.Fatalf("期望批量工具一次准备两个模型操作，实际为: %s", toolResult)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认批量工具操作被处理，handled=%v err=%v", handled, err)
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
	telegramPendingToolActions.Delete(chatID)

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

func TestTelegramAgentFunctionToolUpdatesProviderConfigAfterConfirm(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_update")
	ctx := context.Background()
	chatID := int64(6801293696)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(toolResult, "操作：更新提供商配置：ProviderConfigUpdate") {
		t.Fatalf("期望创建待确认配置更新，实际为: %s", toolResult)
	}
	assertTelegramToolTextHasNoVisibleID(t, toolResult)
	if strings.Contains(toolResult, "new-a") || strings.Contains(toolResult, "new-b") {
		t.Fatalf("待确认消息不应泄露新 api_key，实际为: %s", toolResult)
	}

	var before models.Provider
	if err := db.First(&before, provider.ID).Error; err != nil {
		t.Fatalf("读取确认前提供商失败: %v", err)
	}
	if before.Console != "https://old.example.com" {
		t.Fatalf("确认前不应更新提供商，实际 console=%s", before.Console)
	}

	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望确认配置更新被处理，handled=%v err=%v", handled, err)
	}

	var updated models.Provider
	if err := db.First(&updated, provider.ID).Error; err != nil {
		t.Fatalf("读取确认后提供商失败: %v", err)
	}
	config := map[string]string{}
	if err := json.Unmarshal([]byte(updated.Config), &config); err != nil {
		t.Fatalf("解析确认后 config 失败: %v", err)
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
	if !strings.Contains(client.lastSent(), "已更新提供商配置") {
		t.Fatalf("期望确认后返回更新成功，实际为: %s", client.lastSent())
	}
	assertTelegramToolTextHasNoVisibleID(t, client.lastSent())
}

func TestTelegramAgentToolListOutputsHideIDs(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_list_hide_ids")
	ctx := context.Background()

	model := models.Model{Name: "list-output-model", Status: 1}
	provider := models.Provider{Name: "ListOutputProvider"}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	modelText, err := listTelegramAgentModels(ctx, "list-output")
	if err != nil {
		t.Fatalf("读取模型列表失败: %v", err)
	}
	if !strings.Contains(modelText, "启用｜list-output-model") {
		t.Fatalf("模型列表结构不符合预期: %s", modelText)
	}
	assertTelegramToolTextHasNoVisibleID(t, modelText)

	providerText, err := listTelegramAgentProviders(ctx, "ListOutput")
	if err != nil {
		t.Fatalf("读取提供商列表失败: %v", err)
	}
	if !strings.Contains(providerText, "未关联｜ListOutputProvider｜启用关联 0/0") {
		t.Fatalf("提供商列表结构不符合预期: %s", providerText)
	}
	assertTelegramToolTextHasNoVisibleID(t, providerText)
}

func TestTelegramAgentFunctionToolUpdatesProviderConfigWithoutConfirm(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_update_direct")
	ctx := context.Background()
	chatID := int64(6801293698)
	telegramPendingToolActions.Delete(chatID)

	provider := models.Provider{
		Name:   "DirectProviderConfigUpdate",
		Config: `{"base_url":"https://old.example.com/v1","api_key":"old-key"}`,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	requireConfirm := false
	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{
		ToolConfirmationRequired: &requireConfirm,
	}, telegramAgentOpenAIToolCall{
		ID:   "call_direct_update_provider_config",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolUpdateProviderConfig,
			Arguments: `{"target":"DirectProviderConfigUpdate","config_updates":{"api_key":"11111"}}`,
		},
	})
	if !strings.Contains(toolResult, "已更新提供商配置") {
		t.Fatalf("期望关闭确认后直接执行，实际为: %s", toolResult)
	}
	if hasPendingTelegramToolAction(chatID) {
		t.Fatalf("关闭确认后不应创建待确认操作")
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
	confirmationRequired := false

	modelsToCreate := []models.Model{
		{Name: "claude-auth-key-create", Status: 1},
		{Name: "gpt-auth-key-ignore", Status: 1},
	}
	if err := db.Create(&modelsToCreate).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, 6801293710, models.TelegramAgentConfig{
		ToolConfirmationRequired: &confirmationRequired,
	}, telegramAgentOpenAIToolCall{
		ID:   "call_create_auth_key",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolCreateAuthKey,
			Arguments: `{"name":"TG 创建 Key","allow_all":false,"model_keywords":["claude-auth-key"],"rpm_limit":30}`,
		},
	})
	payload := parseTelegramAgentToolResultPayload(toolResult)
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
	confirmationRequired := false
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

	toolResult := executeTelegramAgentFunctionToolCall(ctx, 6801293711, models.TelegramAgentConfig{
		ToolConfirmationRequired: &confirmationRequired,
	}, telegramAgentOpenAIToolCall{
		ID:   "call_update_auth_key",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolUpdateAuthKey,
			Arguments: `{"target":"TG 待修改 Key","enabled":false,"allow_all":false,"model_keywords":["deepseek-auth-key"],"clear_expires_at":true,"rpm_limit":0}`,
		},
	})
	payload := parseTelegramAgentToolResultPayload(toolResult)
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

func TestTelegramAgentFunctionToolDirectExecutionContinuesToolLoop(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_direct_continue_loop")
	ctx := context.Background()
	chatID := int64(6801293703)
	telegramPendingToolActions.Delete(chatID)

	model := models.Model{Name: "direct-loop-model", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	requireConfirm := false
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
	messages, directFinalText := appendTelegramAgentToolResults(ctx, chatID, models.TelegramAgentConfig{
		ToolConfirmationRequired: &requireConfirm,
	}, toolCalls, nil)
	if len(messages) != 1 {
		t.Fatalf("期望写入 1 条 tool message，实际为 %d", len(messages))
	}
	if directFinalText != "" {
		t.Fatalf("关闭确认后的直接执行不应提前结束工具循环，实际为: %s", directFinalText)
	}
	assertTelegramModelStatus(t, db, model.ID, 0)
}

func TestTelegramAgentFunctionToolPendingActionStopsToolLoop(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_provider_config_stop_loop")
	ctx := context.Background()
	chatID := int64(6801293697)
	telegramPendingToolActions.Delete(chatID)

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
	messages, directFinalText := appendTelegramAgentToolResults(ctx, chatID, models.TelegramAgentConfig{}, toolCalls, nil)
	if len(messages) != 1 {
		t.Fatalf("期望写入 1 条 tool message，实际为 %d", len(messages))
	}
	if !strings.Contains(directFinalText, "操作：更新提供商配置：小米公益") {
		t.Fatalf("期望待确认消息直接结束工具循环，实际为: %s", directFinalText)
	}

	var unchanged models.Provider
	if err := db.First(&unchanged, provider.ID).Error; err != nil {
		t.Fatalf("读取提供商失败: %v", err)
	}
	if strings.Contains(unchanged.Config, "11111") {
		t.Fatalf("未确认前不应写入新 api_key: %s", unchanged.Config)
	}
}

func TestTelegramAgentPendingActionPersistsInDatabase(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_pending_persist")
	ctx := context.Background()
	chatID := int64(6801293701)
	telegramPendingToolActions.Delete(chatID)

	model := models.Model{Name: "persist-confirm-model", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}

	toolResult := executeTelegramAgentFunctionToolCall(ctx, chatID, models.TelegramAgentConfig{}, telegramAgentOpenAIToolCall{
		ID:   "call_persist_pending",
		Type: "function",
		Function: telegramAgentOpenAIFunctionCall{
			Name:      telegramAgentToolSetModelStatus,
			Arguments: `{"target":"persist-confirm-model","enabled":false}`,
		},
	})
	if !strings.Contains(toolResult, "待确认操作") {
		t.Fatalf("期望创建待确认操作，实际为: %s", toolResult)
	}

	var pendingCount int64
	if err := db.Model(&models.TelegramAgentPendingAction{}).Where("chat_id = ?", chatID).Count(&pendingCount).Error; err != nil {
		t.Fatalf("统计待确认操作失败: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("期望待确认操作落库 1 条，实际为 %d", pendingCount)
	}

	telegramPendingToolActions.Delete(chatID)
	client := &telegramToolTestClient{}
	handled, err := HandleTelegramMessage(ctx, client, TelegramMessage{ChatID: chatID, Text: "确认"})
	if err != nil || !handled {
		t.Fatalf("期望清掉内存后仍能确认，handled=%v err=%v", handled, err)
	}
	assertTelegramModelStatus(t, db, model.ID, 0)
	if err := db.Model(&models.TelegramAgentPendingAction{}).Where("chat_id = ?", chatID).Count(&pendingCount).Error; err != nil {
		t.Fatalf("再次统计待确认操作失败: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("确认后待确认操作应删除，实际剩余 %d 条", pendingCount)
	}
}

func TestTelegramAgentToolCallLogMasksSensitiveArguments(t *testing.T) {
	db := setupTelegramAgentToolTestDB(t, "tg_agent_tool_call_log_mask")
	ctx := context.Background()
	chatID := int64(6801293702)
	telegramPendingToolActions.Delete(chatID)

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
	if !strings.Contains(toolResult, "待确认操作") {
		t.Fatalf("期望创建待确认操作，实际为: %s", toolResult)
	}

	var callLog models.TelegramAgentToolCallLog
	if err := db.Where("chat_id = ? AND source = ? AND tool_name = ?", chatID, telegramAgentToolLogSourceFunctionCall, telegramAgentToolUpdateProviderConfig).
		Order("id DESC").
		First(&callLog).Error; err != nil {
		t.Fatalf("读取工具调用日志失败: %v", err)
	}
	if callLog.Status != telegramAgentToolLogStatusPendingConfirmation {
		t.Fatalf("期望工具调用日志状态为待确认，实际为 %s", callLog.Status)
	}
	if strings.Contains(callLog.Arguments, "secret-new-key") {
		t.Fatalf("工具调用日志不应记录 api_key 明文: %s", callLog.Arguments)
	}
	if !strings.Contains(callLog.Arguments, "已隐藏") {
		t.Fatalf("工具调用日志应隐藏敏感字段，实际为: %s", callLog.Arguments)
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
	payload := parseTelegramAgentToolResultPayload(toolResult)
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
	payload := parseTelegramAgentToolResultPayload(toolResult)
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

func TestMergeTelegramAgentConfigPreservesToolConfirmationDisabled(t *testing.T) {
	baseRequireConfirm := true
	overrideRequireConfirm := false
	merged := mergeTelegramAgentConfig(
		models.TelegramAgentConfig{ToolConfirmationRequired: &baseRequireConfirm},
		models.TelegramAgentConfig{ToolConfirmationRequired: &overrideRequireConfirm},
	)
	if telegramAgentRequiresToolConfirmation(merged) {
		t.Fatalf("期望配置覆盖后关闭工具确认")
	}
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
		&models.TelegramAgentPendingAction{},
		&models.TelegramAgentToolCallLog{},
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
