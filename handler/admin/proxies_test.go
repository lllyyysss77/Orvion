package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/models"
)

func TestManagedProxySupportsHTTPAndSOCKS5Only(t *testing.T) {
	tests := []struct {
		value string
		ok    bool
	}{
		{value: "http://127.0.0.1:7890", ok: true},
		{value: "socks5://127.0.0.1:1080", ok: true},
		{value: "socket5://127.0.0.1:1080", ok: true},
		{value: "https://127.0.0.1:7890", ok: false},
		{value: "ftp://127.0.0.1:21", ok: false},
	}
	for _, test := range tests {
		_, err := sanitizeManagedProxyURL(test.value)
		if (err == nil) != test.ok {
			t.Fatalf("sanitizeManagedProxyURL(%q) err=%v", test.value, err)
		}
	}
}

func TestProxyNameIsUniqueIgnoringCaseUntilDeleted(t *testing.T) {
	db := setupProviderAdminTestDB(t, "admin_proxy_unique_name")
	proxy := models.Proxy{Name: "HK-Node", ProxyURL: "http://127.0.0.1:7890"}
	if err := db.Create(&proxy).Error; err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}
	ctx := context.Background()
	if err := ensureProxyNameAvailable(ctx, "hk-node", 0); err == nil {
		t.Fatal("相同节点名应被拒绝")
	}
	if err := ensureProxyNameAvailable(ctx, "HK-Node", proxy.ID); err != nil {
		t.Fatalf("编辑自身时应允许保留节点名: %v", err)
	}
	if err := db.Delete(&proxy).Error; err != nil {
		t.Fatalf("删除代理失败: %v", err)
	}
	if err := ensureProxyNameAvailable(ctx, "hk-node", 0); err != nil {
		t.Fatalf("删除后应允许复用节点名: %v", err)
	}
}

func TestProxyUpdateSyncsProviderAndDeleteRejectsUsage(t *testing.T) {
	db := setupProviderAdminTestDB(t, "admin_proxy_update_sync")
	proxy := models.Proxy{Name: "旧节点", ProxyURL: "http://127.0.0.1:7890"}
	if err := db.Create(&proxy).Error; err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}
	provider := models.Provider{Name: "上游", ProxyID: proxy.ID, ProxyURL: proxy.ProxyURL, Status: 1}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("创建提供商失败: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/proxies/:id", UpdateProxy)
	router.DELETE("/proxies/:id", DeleteProxy)

	updateBody := `{"name":"新节点","proxy_url":"socks5://127.0.0.1:1080"}`
	req := httptest.NewRequest(http.MethodPut, "/proxies/1", strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("更新代理状态码异常: got=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.First(&provider, provider.ID).Error; err != nil {
		t.Fatalf("读取提供商失败: %v", err)
	}
	if provider.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("提供商代理缓存未同步: %q", provider.ProxyURL)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/proxies/1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"code":400`) {
		t.Fatalf("被使用代理应拒绝删除: got=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProviderUsesSelectedProxy(t *testing.T) {
	db := setupProviderAdminTestDB(t, "admin_provider_selected_proxy")
	proxy := models.Proxy{Name: "节点", ProxyURL: "http://127.0.0.1:7890"}
	if err := db.Create(&proxy).Error; err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/providers", CreateProvider)
	payload := map[string]any{
		"name": "带代理上游", "config": `{"base_url":"https://example.com/v1","api_key":"sk-test"}`,
		"proxy_id": proxy.ID, "proxy_url": "http://ignored.invalid:1", "capabilities": []string{"chat"},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/providers", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建提供商状态码异常: got=%d body=%s", rec.Code, rec.Body.String())
	}

	var provider models.Provider
	if err := db.Where("name = ?", "带代理上游").First(&provider).Error; err != nil {
		t.Fatalf("读取提供商失败: %v", err)
	}
	if provider.ProxyID != proxy.ID || provider.ProxyURL != proxy.ProxyURL {
		t.Fatalf("代理关联不正确: proxy_id=%d proxy_url=%q", provider.ProxyID, provider.ProxyURL)
	}
}
