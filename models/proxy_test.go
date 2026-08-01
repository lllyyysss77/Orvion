package models

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestResolveProviderProxyURLsUsesProxyTable(t *testing.T) {
	previousDB := DB
	t.Cleanup(func() { DB = previousDB })
	db, err := gorm.Open(sqlite.Open("file:resolve_provider_proxies?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("初始化数据库失败: %v", err)
	}
	DB = db
	if err := db.AutoMigrate(&Proxy{}, &Provider{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	proxy := Proxy{Name: "节点", ProxyURL: "http://127.0.0.1:7890"}
	if err := db.Create(&proxy).Error; err != nil {
		t.Fatalf("创建代理失败: %v", err)
	}
	providers := []Provider{
		{ProxyID: proxy.ID, ProxyURL: "http://stale.example:8080"},
		{ProxyURL: "socks5://legacy.example:1080"},
	}
	if err := ResolveProviderProxyURLs(context.Background(), providers); err != nil {
		t.Fatalf("解析代理失败: %v", err)
	}
	if providers[0].ProxyURL != proxy.ProxyURL {
		t.Fatalf("未使用代理表地址: %q", providers[0].ProxyURL)
	}
	if providers[1].ProxyURL != "socks5://legacy.example:1080" {
		t.Fatalf("旧代理地址不应改变: %q", providers[1].ProxyURL)
	}
}
