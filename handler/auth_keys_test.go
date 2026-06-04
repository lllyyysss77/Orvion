package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestUpdateAuthKeyPersistsZeroValueFields(t *testing.T) {
	db := setupAuthKeyHandlerTestDB(t, "handler_auth_key_update_zero_values")
	router := setupAuthKeyHandlerTestRouter()

	expiresAt := time.Now().Add(24 * time.Hour)
	authKey := models.AuthKey{
		Name:      "无限制 Key",
		Key:       "sk-update-zero-values",
		Status:    1,
		AllowAll:  1,
		Models:    "[]",
		ExpiresAt: &expiresAt,
		RpmLimit:  60,
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("创建 AuthKey 失败: %v", err)
	}

	body := `{"name":"限制 Key","status":false,"allow_all":false,"models":["gpt-5.5"],"expires_at":null,"rpm_limit":0}`
	req := httptest.NewRequest(http.MethodPut, "/auth-keys/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("更新状态码异常: got=%d body=%s", rec.Code, rec.Body.String())
	}

	var updated models.AuthKey
	if err := db.First(&updated, authKey.ID).Error; err != nil {
		t.Fatalf("读取更新后 AuthKey 失败: %v", err)
	}
	if updated.AllowAll != 0 {
		t.Fatalf("allow_all 应更新为 0，实际为 %d", updated.AllowAll)
	}
	if updated.Status != 0 {
		t.Fatalf("status 应更新为 0，实际为 %d", updated.Status)
	}
	if updated.RpmLimit != 0 {
		t.Fatalf("rpm_limit 应更新为 0，实际为 %d", updated.RpmLimit)
	}
	if updated.ExpiresAt != nil {
		t.Fatalf("expires_at 应清空，实际为 %v", updated.ExpiresAt)
	}
	if updated.Models != `["gpt-5.5"]` {
		t.Fatalf("models 未正确更新，实际为 %s", updated.Models)
	}
}

func TestToggleAuthKeyStatusPersistsDisabledStatus(t *testing.T) {
	db := setupAuthKeyHandlerTestDB(t, "handler_auth_key_toggle_zero_status")
	router := setupAuthKeyHandlerTestRouter()

	authKey := models.AuthKey{
		Name:     "待禁用 Key",
		Key:      "sk-toggle-zero-status",
		Status:   1,
		AllowAll: 1,
		Models:   "[]",
	}
	if err := db.Create(&authKey).Error; err != nil {
		t.Fatalf("创建 AuthKey 失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/auth-keys/1/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("切换状态码异常: got=%d body=%s", rec.Code, rec.Body.String())
	}

	var updated models.AuthKey
	if err := db.First(&updated, authKey.ID).Error; err != nil {
		t.Fatalf("读取更新后 AuthKey 失败: %v", err)
	}
	if updated.Status != 0 {
		t.Fatalf("status 应切换为 0，实际为 %d", updated.Status)
	}
}

func setupAuthKeyHandlerTestDB(t *testing.T, name string) *gorm.DB {
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
	if err := db.AutoMigrate(&models.AuthKey{}); err != nil {
		t.Fatalf("迁移 AuthKey 表失败: %v", err)
	}
	return db
}

func setupAuthKeyHandlerTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/auth-keys/:id", UpdateAuthKey)
	router.PUT("/auth-keys/:id/status", ToggleAuthKeyStatus)
	return router
}
