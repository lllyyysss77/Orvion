package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestNormalizeTelegramAgentDirectModelsBaseURL(t *testing.T) {
	baseURL, err := normalizeTelegramAgentDirectModelsBaseURL("https://api.example.com/v1/chat/completions?unused=1")
	if err != nil {
		t.Fatalf("归一化 TG Agent 模型 URL 失败: %v", err)
	}
	if baseURL != "https://api.example.com/v1" {
		t.Fatalf("URL 归一化不正确: %s", baseURL)
	}
}

func TestUpdateProviderPersistsEmptyProxyURL(t *testing.T) {
	db := setupProviderAdminTestDB(t, "admin_provider_update_empty_proxy")
	router := setupProviderAdminTestRouter()

	provider := models.Provider{
		Name:                       "待清空代理",
		Config:                     `{"api_key":"sk-old","base_url":"https://old.example.com/v1"}`,
		Console:                    "https://console.example.com",
		ProxyURL:                   "http://127.0.0.1:7890",
		ModelsFetchMode:            modelsFetchModeV1Models,
		Capabilities:               models.ProviderCapabilities{"chat", "openai"},
		Status:                     1,
		InterfaceConversionEnabled: 1,
		InterfaceConversionTarget:  "chat",
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建 Provider 失败: %v", err)
	}

	body := `{"name":"已清空代理","config":"{\"api_key\":\"sk-new\",\"base_url\":\"https://new.example.com/v1\"}","console":"","proxy_url":"","models_fetch_mode":"api_pricing","capabilities":["chat","openai"],"interface_conversion_enabled":false,"interface_conversion_target":""}`
	req := httptest.NewRequest(http.MethodPut, "/providers/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("更新状态码异常: got=%d body=%s", rec.Code, rec.Body.String())
	}

	var updated models.Provider
	if err := db.First(&updated, provider.ID).Error; err != nil {
		t.Fatalf("读取更新后 Provider 失败: %v", err)
	}
	if updated.ProxyURL != "" {
		t.Fatalf("proxy_url 应清空，实际为 %q", updated.ProxyURL)
	}
	if updated.Console != "" {
		t.Fatalf("console 应清空，实际为 %q", updated.Console)
	}
	if updated.InterfaceConversionEnabled != 0 {
		t.Fatalf("interface_conversion_enabled 应更新为 0，实际为 %d", updated.InterfaceConversionEnabled)
	}
	if updated.InterfaceConversionTarget != "" {
		t.Fatalf("interface_conversion_target 应清空，实际为 %q", updated.InterfaceConversionTarget)
	}
	if updated.ModelsFetchMode != modelsFetchModePricing {
		t.Fatalf("models_fetch_mode 未正确更新，实际为 %q", updated.ModelsFetchMode)
	}
}

func setupProviderAdminTestDB(t *testing.T, name string) *gorm.DB {
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
	if err := db.AutoMigrate(&models.Provider{}); err != nil {
		t.Fatalf("迁移 Provider 表失败: %v", err)
	}
	return db
}

func setupProviderAdminTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/providers/:id", UpdateProvider)
	return router
}
