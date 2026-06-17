package service

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func TestScheduleModelProviderAutoDisableCheckDeduplicatesByProvider(t *testing.T) {
	oldDB := models.DB
	oldQueue := autoDisableQueue
	oldPending := autoDisablePending
	oldProviders := autoDisableProviders

	t.Cleanup(func() {
		models.DB = oldDB
		autoDisableQueue = oldQueue
		autoDisablePending = oldPending
		autoDisableProviders = oldProviders
	})

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "model-provider-guard.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	models.DB = db
	if err := db.AutoMigrate(&models.Provider{}, &models.Model{}, &models.ModelWithProvider{}); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	provider := models.Provider{Name: "共享提供商", Status: 1}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	firstModel := models.Model{Name: "模型A", Status: 1}
	secondModel := models.Model{Name: "模型B", Status: 1}
	if err := db.Create(&firstModel).Error; err != nil {
		t.Fatalf("create first model: %v", err)
	}
	if err := db.Create(&secondModel).Error; err != nil {
		t.Fatalf("create second model: %v", err)
	}

	firstBinding := models.ModelWithProvider{ModelID: firstModel.ID, ProviderID: provider.ID, ProviderModel: "a", Status: 1}
	secondBinding := models.ModelWithProvider{ModelID: secondModel.ID, ProviderID: provider.ID, ProviderModel: "b", Status: 1}
	if err := db.Create(&firstBinding).Error; err != nil {
		t.Fatalf("create first binding: %v", err)
	}
	if err := db.Create(&secondBinding).Error; err != nil {
		t.Fatalf("create second binding: %v", err)
	}

	autoDisableQueue = make(chan uint, 8)
	autoDisablePending = make(map[uint]struct{})
	autoDisableProviders = make(map[uint]struct{})

	ScheduleModelProviderAutoDisableCheck(firstBinding.ID)
	ScheduleModelProviderAutoDisableCheck(secondBinding.ID)

	if len(autoDisablePending) != 1 {
		t.Fatalf("同一提供商并发调度时应只保留一个待处理任务，实际为 %d", len(autoDisablePending))
	}
	if len(autoDisableProviders) != 1 {
		t.Fatalf("同一提供商并发调度时应只保留一个提供商锁，实际为 %d", len(autoDisableProviders))
	}

	if _, exists := autoDisablePending[firstBinding.ID]; !exists {
		t.Fatalf("应保留首个模型关联的待处理标记")
	}
	if _, exists := autoDisablePending[secondBinding.ID]; exists {
		t.Fatalf("同一提供商的第二个模型关联不应重复进入待处理集合")
	}
	if _, exists := autoDisableProviders[provider.ID]; !exists {
		t.Fatalf("应保留该提供商的去重锁")
	}
}
