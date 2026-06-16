package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestDeleteTelegramAgentSessionDeletesConversationRecords(t *testing.T) {
	previousDB := models.DB
	t.Cleanup(func() {
		models.DB = previousDB
	})

	db, err := gorm.Open(sqlite.Open("file:admin_tg_agent_delete_session?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(
		&models.TelegramAgentMessage{},
		&models.TelegramAgentSession{},
		&models.TelegramAgentToolCallLog{},
	); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}

	conversationID := "tg-6801293687-test"
	chatID := int64(6801293687)
	if err := db.Create(&models.TelegramAgentSession{ChatID: chatID, ConversationID: conversationID}).Error; err != nil {
		t.Fatalf("写入会话失败: %v", err)
	}
	if err := db.Create(&models.TelegramAgentMessage{ChatID: chatID, ConversationID: conversationID, MessageOrder: 0, Role: "user", Content: "hello"}).Error; err != nil {
		t.Fatalf("写入消息失败: %v", err)
	}
	if err := db.Create(&models.TelegramAgentToolCallLog{ChatID: chatID, ConversationID: conversationID, Source: "function_call", ToolName: "read_system_logs", Status: "completed"}).Error; err != nil {
		t.Fatalf("写入工具日志失败: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/api/tg-agent/sessions/:conversation_id", DeleteTelegramAgentSession)
	req := httptest.NewRequest(http.MethodDelete, "/api/tg-agent/sessions/"+conversationID, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际为 %d: %s", recorder.Code, recorder.Body.String())
	}

	assertNoRowsForTelegramAgentSessionDelete(t, db, &models.TelegramAgentMessage{}, "conversation_id = ?", conversationID)
	assertNoRowsForTelegramAgentSessionDelete(t, db, &models.TelegramAgentToolCallLog{}, "conversation_id = ?", conversationID)
	assertNoRowsForTelegramAgentSessionDelete(t, db, &models.TelegramAgentSession{}, "conversation_id = ?", conversationID)
}

func TestDeleteTelegramAgentSessionDeletesUnrecordedConversation(t *testing.T) {
	previousDB := models.DB
	t.Cleanup(func() {
		models.DB = previousDB
	})

	db, err := gorm.Open(sqlite.Open("file:admin_tg_agent_delete_unrecorded_session?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(
		&models.TelegramAgentMessage{},
		&models.TelegramAgentSession{},
		&models.TelegramAgentToolCallLog{},
	); err != nil {
		t.Fatalf("迁移测试表失败: %v", err)
	}

	chatID := int64(6801293687)
	if err := db.Create(&models.TelegramAgentMessage{ChatID: chatID, MessageOrder: 0, Role: "user", Content: "old"}).Error; err != nil {
		t.Fatalf("写入旧消息失败: %v", err)
	}
	if err := db.Create(&models.TelegramAgentToolCallLog{ChatID: chatID, Source: "function_call", ToolName: "read_system_logs", Status: "completed"}).Error; err != nil {
		t.Fatalf("写入旧工具日志失败: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.DELETE("/api/tg-agent/sessions", DeleteTelegramAgentSession)
	req := httptest.NewRequest(http.MethodDelete, "/api/tg-agent/sessions?chat_id=6801293687", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("期望 HTTP 200，实际为 %d: %s", recorder.Code, recorder.Body.String())
	}

	assertNoRowsForTelegramAgentSessionDelete(t, db, &models.TelegramAgentMessage{}, "chat_id = ? AND (conversation_id = ? OR conversation_id IS NULL)", chatID, "")
	assertNoRowsForTelegramAgentSessionDelete(t, db, &models.TelegramAgentToolCallLog{}, "chat_id = ? AND (conversation_id = ? OR conversation_id IS NULL)", chatID, "")
}

func assertNoRowsForTelegramAgentSessionDelete(t *testing.T, db *gorm.DB, model any, query string, args ...any) {
	t.Helper()
	var count int64
	if err := db.Model(model).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("统计删除结果失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("期望记录已删除，仍剩余 %d 条", count)
	}
}
