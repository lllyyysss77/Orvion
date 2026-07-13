package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service/ifacebridge"
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

func TestBalanceChatInternalSwitchesProviderAfterFallbackable4xx(t *testing.T) {
	db := setupBalanceChatRetryTestDB(t, "fallbackable_4xx")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"content":"should-not-call"}}]}`)
	}))
	t.Cleanup(server.Close)

	meta := buildBalanceChatRetryTestMeta(t, server.URL, 2, 4)
	before := Before{
		Model:  "primary-model",
		Stream: false,
		raw:    []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`),
	}

	res, _, err := balanceChatInternal(nil, time.Now(), consts.StyleOpenAI, "/v1/chat/completions", before, meta, models.ReqMeta{}, false)
	if err != nil {
		t.Fatalf("首个提供商返回 400 后应切换到下一提供商并成功，实际 err=%v", err)
	}
	if res == nil || res.Body == nil {
		t.Fatal("期望返回第二个提供商的成功响应")
	}
	_ = res.Body.Close()
	if requestCount != 2 {
		t.Fatalf("400 不应重试当前提供商，但应切换下一提供商，实际请求次数=%d", requestCount)
	}
	waitForBalanceChatRetryLogRows(t, db, 1)
}

func TestBalanceChatInternalReturnsFallbackable4xxAfterAllProvidersFail(t *testing.T) {
	db := setupBalanceChatRetryTestDB(t, "fallbackable_4xx_all_failed")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	meta := buildBalanceChatRetryTestMeta(t, server.URL, 2, 4)
	before := Before{
		Model:  "primary-model",
		Stream: false,
		raw:    []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`),
	}

	_, _, err := balanceChatInternal(nil, time.Now(), consts.StyleOpenAI, "/v1/chat/completions", before, meta, models.ReqMeta{}, false)
	if err == nil {
		t.Fatal("所有提供商返回 400 后应返回错误")
	}
	if statusCode, ok := UpstreamStatusCode(err); !ok || statusCode != http.StatusBadRequest {
		t.Fatalf("期望保留最后的 400 状态，实际 status=%d ok=%v err=%v", statusCode, ok, err)
	}
	if !errors.Is(err, errMaximumRetryAttemptsReached) {
		t.Fatalf("400 应保留模型回退哨兵错误，实际 err=%v", err)
	}
	if requestCount != 2 {
		t.Fatalf("应各请求两个候选提供商一次，实际请求次数=%d", requestCount)
	}
	waitForBalanceChatRetryLogRows(t, db, 2)
}

func TestBalanceChatWithFallbackUsesFallbackModelForConfigured4xx(t *testing.T) {
	db := setupBalanceChatRetryTestDB(t, "fallback_model_for_4xx")
	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}); err != nil {
		t.Fatalf("migrate fallback tables: %v", err)
	}

	primaryRequestCount := 0
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryRequestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`)
	}))
	t.Cleanup(primaryServer.Close)

	fallbackRequestCount := 0
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackRequestCount++
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"fallback-upstream-model"`) {
			t.Fatalf("回退上游请求应替换为 fallback-upstream-model，实际 body=%s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"content":"fallback ok"}}]}`)
	}))
	t.Cleanup(fallbackServer.Close)

	config, err := json.Marshal(map[string]string{
		"base_url": fallbackServer.URL,
		"api_key":  "sk-test",
	})
	if err != nil {
		t.Fatalf("marshal fallback provider config: %v", err)
	}
	fallbackModel := models.Model{
		Name:     "fallback-model",
		Status:   1,
		MaxRetry: 2,
		TimeOut:  5,
		Strategy: consts.BalancerRotor,
	}
	if err := db.Create(&fallbackModel).Error; err != nil {
		t.Fatalf("create fallback model: %v", err)
	}
	fallbackProvider := models.Provider{
		Name:   "fallback-provider",
		Status: 1,
		Config: string(config),
	}
	if err := db.Create(&fallbackProvider).Error; err != nil {
		t.Fatalf("create fallback provider: %v", err)
	}
	if err := db.Create(&models.ModelWithProvider{
		ModelID:       fallbackModel.ID,
		ProviderID:    fallbackProvider.ID,
		ProviderModel: "fallback-upstream-model",
		Status:        1,
		Weight:        1,
	}).Error; err != nil {
		t.Fatalf("create fallback model provider: %v", err)
	}

	meta := buildBalanceChatRetryTestMeta(t, primaryServer.URL, 1, 2)
	meta.ModelID = 100
	meta.FallbackModelID = fallbackModel.ID
	before := Before{
		Model:  "primary-model",
		Stream: false,
		raw:    []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`),
	}

	res, _, effectiveBefore, _, err := balanceChatWithFallback(nil, time.Now(), consts.StyleOpenAI, "/v1/chat/completions", before, meta, models.ReqMeta{}, false, make(map[uint]struct{}))
	if err != nil {
		t.Fatalf("400 应触发回退模型并成功，实际 err=%v", err)
	}
	if res == nil || res.Body == nil {
		t.Fatalf("期望返回回退模型成功响应")
	}
	_ = res.Body.Close()
	if effectiveBefore.Model != "fallback-model" {
		t.Fatalf("期望最终使用 fallback-model，实际=%s", effectiveBefore.Model)
	}
	if primaryRequestCount != 1 {
		t.Fatalf("400 不应重试主模型，主模型请求次数=%d", primaryRequestCount)
	}
	if fallbackRequestCount != 1 {
		t.Fatalf("应请求一次回退模型，实际=%d", fallbackRequestCount)
	}
	waitForBalanceChatRetryLogRows(t, db, 1)
}

func TestBalanceChatInternalRetries429AndContinuesProviderScheduling(t *testing.T) {
	db := setupBalanceChatRetryTestDB(t, "retryable_429")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"invalid_request_error","code":"insufficient_quota"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(server.Close)

	meta := buildBalanceChatRetryTestMeta(t, server.URL, 2, 3)
	before := Before{
		Model:  "primary-model",
		Stream: false,
		raw:    []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`),
	}

	res, _, err := balanceChatInternal(nil, time.Now(), consts.StyleOpenAI, "/v1/chat/completions", before, meta, models.ReqMeta{}, false)
	if err != nil {
		t.Fatalf("429 应进入重试/切换提供商流程后成功，实际 err=%v", err)
	}
	if res == nil || res.Body == nil {
		t.Fatalf("期望返回成功响应")
	}
	_ = res.Body.Close()
	if requestCount != 3 {
		t.Fatalf("429 应先重试当前提供商再继续调度，实际请求次数=%d", requestCount)
	}
	waitForBalanceChatRetryLogRows(t, db, 2)
}

func TestBalanceChatInternalRetriesRetryable5xx(t *testing.T) {
	db := setupBalanceChatRetryTestDB(t, "retryable_5xx")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			http.Error(w, `{"error":"bad gateway"}`, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"ok","choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(server.Close)

	meta := buildBalanceChatRetryTestMeta(t, server.URL, 1, 2)
	before := Before{
		Model:  "primary-model",
		Stream: false,
		raw:    []byte(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}]}`),
	}

	res, _, err := balanceChatInternal(nil, time.Now(), consts.StyleOpenAI, "/v1/chat/completions", before, meta, models.ReqMeta{}, false)
	if err != nil {
		t.Fatalf("5xx 应允许重试后成功，实际 err=%v", err)
	}
	if res == nil || res.Body == nil {
		t.Fatalf("期望返回成功响应")
	}
	_ = res.Body.Close()
	if requestCount != 2 {
		t.Fatalf("5xx 应重试一次后成功，实际请求次数=%d", requestCount)
	}
	waitForBalanceChatRetryLogRows(t, db, 1)
}

func setupBalanceChatRetryTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	oldDB := models.DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name+".db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	models.DB = db
	t.Cleanup(func() {
		models.DB = oldDB
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&models.ChatLog{}, &models.Config{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	if err := db.Table(models.ChatLogMonthlyTableName(time.Now())).AutoMigrate(&models.ChatLog{}); err != nil {
		t.Fatalf("migrate monthly log table: %v", err)
	}
	return db
}

func buildBalanceChatRetryTestMeta(t *testing.T, baseURL string, providerCount int, maxRetry int) *ProvidersWithMeta {
	t.Helper()
	config, err := json.Marshal(map[string]string{
		"base_url": baseURL,
		"api_key":  "sk-test",
	})
	if err != nil {
		t.Fatalf("marshal provider config: %v", err)
	}
	meta := &ProvidersWithMeta{
		ModelWithProviderMap: map[uint]models.ModelWithProvider{},
		WeightItems:          map[uint]int{},
		ProviderMap:          map[uint]models.Provider{},
		BridgePlans:          map[uint]ifacebridge.Plan{},
		ModelName:            "primary-model",
		MaxRetry:             maxRetry,
		TimeOut:              5,
		Strategy:             consts.BalancerRotor,
	}
	for index := 1; index <= providerCount; index++ {
		providerID := uint(index)
		modelWithProviderID := uint(index)
		meta.ProviderMap[providerID] = models.Provider{
			ID:     providerID,
			Name:   fmt.Sprintf("provider-%d", index),
			Config: string(config),
			Status: 1,
		}
		meta.ModelWithProviderMap[modelWithProviderID] = models.ModelWithProvider{
			ID:            modelWithProviderID,
			ProviderID:    providerID,
			ProviderModel: "upstream-model",
			Status:        1,
			Weight:        1,
		}
		meta.WeightItems[modelWithProviderID] = 1
	}
	return meta
}

func waitForBalanceChatRetryLogRows(t *testing.T, db *gorm.DB, expected int64) {
	t.Helper()
	tableName := models.ChatLogMonthlyTableName(time.Now())
	deadline := time.Now().Add(time.Second)
	for {
		var count int64
		err := db.Table(tableName).Count(&count).Error
		if err == nil && count >= expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待重试日志落库超时: count=%d expected=%d err=%v", count, expected, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
