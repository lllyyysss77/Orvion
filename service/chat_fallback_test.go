package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestProvidersWithMetaByModelAllowsFallbackWhenNoEnabledProviders(t *testing.T) {
	oldDB := models.DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "chat-fallback.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	models.DB = db
	t.Cleanup(func() {
		models.DB = oldDB
	})

	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	primary := models.Model{
		Name:            "primary-model",
		Status:          1,
		FallbackModelID: 2,
		MaxRetry:        2,
		TimeOut:         30,
		Strategy:        "lottery",
	}
	fallback := models.Model{
		ID:       2,
		Name:     "fallback-model",
		Status:   1,
		MaxRetry: 2,
		TimeOut:  30,
		Strategy: "lottery",
	}
	if err := db.Create(&primary).Error; err != nil {
		t.Fatalf("create primary model: %v", err)
	}
	if err := db.Create(&fallback).Error; err != nil {
		t.Fatalf("create fallback model: %v", err)
	}

	provider := models.Provider{Name: "disabled-provider", Status: 1}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	binding := models.ModelWithProvider{
		ModelID:       primary.ID,
		ProviderID:    provider.ID,
		ProviderModel: "primary-upstream",
		Status:        0,
		Weight:        1,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create model binding: %v", err)
	}

	meta, err := providersWithMetaByModel(context.Background(), "/v1/chat/completions", primary)
	if err != nil {
		t.Fatalf("providersWithMetaByModel should allow fallback, got err=%v", err)
	}
	if meta == nil {
		t.Fatalf("providersWithMetaByModel returned nil meta")
	}
	if meta.FallbackModelID != fallback.ID {
		t.Fatalf("fallback model id mismatch: got=%d want=%d", meta.FallbackModelID, fallback.ID)
	}
	if len(meta.WeightItems) != 0 {
		t.Fatalf("weight items should be empty when no enabled providers, got=%d", len(meta.WeightItems))
	}
}

func TestBalanceChatInternalReturnsRetryErrorWhenNoProviderCandidates(t *testing.T) {
	meta := &ProvidersWithMeta{
		WeightItems:     map[uint]int{},
		ProviderMap:     map[uint]models.Provider{},
		ModelName:       "primary-model",
		FallbackModelID: 2,
		MaxRetry:        2,
		TimeOut:         30,
		Strategy:        "lottery",
	}
	before := Before{
		Model:  "primary-model",
		Stream: false,
		raw:    []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`),
	}

	_, _, err := balanceChatInternal(nil, time.Now(), "openai", "/v1/chat/completions", before, meta, models.ReqMeta{}, true)
	if err == nil {
		t.Fatalf("balanceChatInternal should return error when no provider candidates")
	}
	if err != errMaximumRetryAttemptsReached {
		t.Fatalf("unexpected error: got=%v want=%v", err, errMaximumRetryAttemptsReached)
	}
}
