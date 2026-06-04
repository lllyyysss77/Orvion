package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

func loadTelegramPendingToolAction(ctx context.Context, chatID int64) (telegramToolAction, bool, error) {
	if models.DB == nil {
		action, ok := loadTelegramPendingToolActionFromMemory(chatID)
		return action, ok, nil
	}

	var row models.TelegramAgentPendingAction
	err := models.DB.WithContext(ctx).Where("chat_id = ?", chatID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return telegramToolAction{}, false, nil
		}
		if action, ok := loadTelegramPendingToolActionFromMemory(chatID); ok {
			return action, true, nil
		}
		return telegramToolAction{}, false, err
	}
	if !row.ExpiresAt.IsZero() && time.Now().After(row.ExpiresAt) {
		deleteTelegramPendingToolAction(ctx, chatID)
		return telegramToolAction{}, false, nil
	}

	action, err := decodeTelegramToolAction(row.ActionJSON)
	if err != nil {
		deleteTelegramPendingToolAction(ctx, chatID)
		return telegramToolAction{}, false, err
	}
	action.ChatID = chatID
	if action.CreatedAt.IsZero() {
		action.CreatedAt = row.CreatedAt
	}
	return action, true, nil
}

func loadTelegramPendingToolActionFromMemory(chatID int64) (telegramToolAction, bool) {
	value, ok := telegramPendingToolActions.Load(chatID)
	if !ok {
		return telegramToolAction{}, false
	}
	action, ok := value.(telegramToolAction)
	if !ok {
		telegramPendingToolActions.Delete(chatID)
		return telegramToolAction{}, false
	}
	if time.Since(action.CreatedAt) > telegramAgentToolConfirmTTL {
		telegramPendingToolActions.Delete(chatID)
		return telegramToolAction{}, false
	}
	return action, true
}

func saveTelegramPendingToolAction(ctx context.Context, action telegramToolAction) error {
	if action.CreatedAt.IsZero() {
		action.CreatedAt = time.Now()
	}
	raw, err := encodeTelegramToolAction(action)
	if err != nil {
		return err
	}
	if models.DB == nil {
		telegramPendingToolActions.Store(action.ChatID, action)
		return nil
	}

	expiresAt := action.CreatedAt.Add(telegramAgentToolConfirmTTL)
	var existing models.TelegramAgentPendingAction
	err = models.DB.WithContext(ctx).Where("chat_id = ?", action.ChatID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.DB.WithContext(ctx).Create(&models.TelegramAgentPendingAction{
				ChatID:     action.ChatID,
				ActionJSON: raw,
				Summary:    action.Summary,
				ExpiresAt:  expiresAt,
			}).Error
		}
		return err
	}

	return models.DB.WithContext(ctx).
		Model(&models.TelegramAgentPendingAction{}).
		Where("id = ?", existing.ID).
		Updates(map[string]any{
			"action_json": raw,
			"summary":     action.Summary,
			"expires_at":  expiresAt,
		}).Error
}

func deleteTelegramPendingToolAction(ctx context.Context, chatID int64) {
	telegramPendingToolActions.Delete(chatID)
	if models.DB == nil {
		return
	}
	_ = models.DB.WithContext(ctx).Where("chat_id = ?", chatID).Delete(&models.TelegramAgentPendingAction{}).Error
}

func encodeTelegramToolAction(action telegramToolAction) (string, error) {
	raw, err := json.Marshal(action)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeTelegramToolAction(raw string) (telegramToolAction, error) {
	var action telegramToolAction
	if err := json.Unmarshal([]byte(raw), &action); err != nil {
		return telegramToolAction{}, err
	}
	return action, nil
}
