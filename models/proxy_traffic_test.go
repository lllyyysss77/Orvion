package models

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestQueryChatLogProxyTrafficFiltersTodayAndProxy(t *testing.T) {
	previousDB := DB
	clearChatLogMonthlyTableCacheForTest()
	t.Cleanup(func() {
		DB = previousDB
		clearChatLogMonthlyTableCacheForTest()
	})

	dialector, err := buildDialector(filepath.Join(t.TempDir(), "proxy-traffic.db"))
	if err != nil {
		t.Fatalf("build dialector: %v", err)
	}
	DB, err = gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := DB.AutoMigrate(&Proxy{}, &Provider{}, &ModelWithProvider{}); err != nil {
		t.Fatalf("migrate proxy relations: %v", err)
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	proxy := Proxy{Name: "节点", ProxyURL: "http://127.0.0.1:7890"}
	if err := DB.Create(&proxy).Error; err != nil {
		t.Fatalf("create proxy: %v", err)
	}
	provider := Provider{Name: "上游", ProxyID: proxy.ID}
	if err := DB.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	mwp := ModelWithProvider{ProviderID: provider.ID, ProviderModel: "gpt-test"}
	if err := DB.Create(&mwp).Error; err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	withoutProxy := ModelWithProvider{ProviderModel: "direct"}
	if err := DB.Create(&withoutProxy).Error; err != nil {
		t.Fatalf("create direct model provider: %v", err)
	}

	logs := []ChatLog{
		{UUID: "proxy-today", CreatedAt: start.Add(2 * time.Hour), UpdatedAt: start.Add(2 * time.Hour), ModelWithProviderID: mwp.ID, TrafficBytes: 1200},
		{UUID: "proxy-yesterday", CreatedAt: start.Add(-time.Hour), UpdatedAt: start.Add(-time.Hour), ModelWithProviderID: mwp.ID, TrafficBytes: 9000},
		{UUID: "direct-today", CreatedAt: start.Add(3 * time.Hour), UpdatedAt: start.Add(3 * time.Hour), ModelWithProviderID: withoutProxy.ID, TrafficBytes: 5000},
	}
	for _, log := range logs {
		if tableName, err := EnsureChatLogMonthlyTable(log.CreatedAt); err != nil {
			t.Fatalf("ensure chat log table: %v", err)
		} else if err := DB.Table(tableName).Create(&log).Error; err != nil {
			t.Fatalf("create chat log: %v", err)
		}
	}

	rows, err := QueryChatLogProxyTraffic(context.Background(), start, start.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("query proxy traffic: %v", err)
	}
	if len(rows) != 1 || rows[0].ProxyID != proxy.ID || rows[0].TrafficBytes != 1200 {
		t.Fatalf("unexpected proxy traffic rows: %+v", rows)
	}
}
