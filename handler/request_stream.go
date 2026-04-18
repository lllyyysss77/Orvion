package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
)

type RequestStreamEvent struct {
	ID               uint   `json:"id"`
	CreatedAt        string `json:"created_at"`
	AuthKeyID        uint   `json:"auth_key_id"`
	AuthKeyName      string `json:"auth_key_name"`
	ProviderName     string `json:"provider_name"`
	ModelName        string `json:"model_name"`
	Status           string `json:"status"`
	LatencyMs        int    `json:"latency_ms"`
	ProxyTimeMs      int    `json:"proxy_time_ms"`
	FirstChunkTimeMs int    `json:"first_chunk_time_ms"`
	ChunkTimeMs      int    `json:"chunk_time_ms"`
	StreamLike       bool   `json:"stream_like"`
}

type requestStreamDBRow struct {
	ID               uint      `gorm:"column:id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	AuthKeyID        uint      `gorm:"column:auth_key_id"`
	ProviderName     string    `gorm:"column:provider_name"`
	ModelName        string    `gorm:"column:model_name"`
	Status           string    `gorm:"column:status"`
	ProxyTimeMs      int       `gorm:"column:proxy_time_ms"`
	FirstChunkTimeMs int       `gorm:"column:first_chunk_time_ms"`
	ChunkTimeMs      int       `gorm:"column:chunk_time_ms"`
}

// StreamRequests 按 chat_logs 的递增 ID 推送请求流（SSE）。
// 典型用法：/api/stream/requests?after_id=123
func StreamRequests(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		common.InternalServerError(c, "streaming is not supported by this server")
		return
	}

	afterIDRaw := strings.TrimSpace(c.Query("after_id"))
	afterID, err := parseAfterID(afterIDRaw)
	if err != nil {
		common.BadRequest(c, "Invalid after_id")
		return
	}
	startMode := "replay"
	if afterIDRaw == "" {
		latestID, latestErr := queryLatestRequestLogID(c.Request.Context())
		if latestErr != nil {
			common.InternalServerError(c, "Failed to query latest log id: "+latestErr.Error())
			return
		}
		afterID = latestID
		startMode = "realtime"
	}
	batch := parsePositiveInt(c.Query("batch"), 80, 300)
	if batch < 10 {
		batch = 10
	}
	pollMs := parsePositiveInt(c.Query("poll_ms"), 1200, 5000)
	if pollMs < 200 {
		pollMs = 200
	}
	heartbeatSec := parsePositiveInt(c.Query("heartbeat_sec"), 15, 60)
	if heartbeatSec < 5 {
		heartbeatSec = 5
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher.Flush()

	lastID := afterID
	_ = writeSSEEvent(c.Writer, flusher, "hello", gin.H{
		"last_id":   lastID,
		"mode":      startMode,
		"poll_ms":   pollMs,
		"batch":     batch,
		"server_at": time.Now().Format(time.RFC3339Nano),
	})

	pollTicker := time.NewTicker(time.Duration(pollMs) * time.Millisecond)
	defer pollTicker.Stop()
	heartbeatTicker := time.NewTicker(time.Duration(heartbeatSec) * time.Second)
	defer heartbeatTicker.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			events, newestID, queryErr := queryIncrementalRequestEvents(ctx, lastID, batch)
			if queryErr != nil {
				_ = writeSSEEvent(c.Writer, flusher, "error", gin.H{
					"message": queryErr.Error(),
				})
				continue
			}
			if len(events) == 0 {
				continue
			}
			for _, event := range events {
				if err := writeSSEEvent(c.Writer, flusher, "request", event); err != nil {
					return
				}
			}
			lastID = newestID
		case <-heartbeatTicker.C:
			if err := writeSSEEvent(c.Writer, flusher, "heartbeat", gin.H{
				"last_id": lastID,
				"at":      time.Now().Format(time.RFC3339Nano),
			}); err != nil {
				return
			}
		}
	}
}

func parseAfterID(raw string) (uint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(parsed), nil
}

func queryLatestRequestLogID(ctx context.Context) (uint, error) {
	type latestRow struct {
		LatestID uint `gorm:"column:latest_id"`
	}
	var row latestRow
	if err := models.DB.WithContext(ctx).
		Table("chat_logs").
		Select("COALESCE(MAX(id), 0) AS latest_id").
		Where("deleted_at IS NULL").
		Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.LatestID, nil
}

func queryIncrementalRequestEvents(ctx context.Context, afterID uint, limit int) ([]RequestStreamEvent, uint, error) {
	rows := make([]requestStreamDBRow, 0, limit)
	if err := models.DB.WithContext(ctx).
		Table("chat_logs").
		Select("id, created_at, auth_key_id, provider_name, name AS model_name, status, proxy_time_ms, first_chunk_time_ms, chunk_time_ms").
		Where("deleted_at IS NULL AND id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, afterID, err
	}

	if len(rows) == 0 {
		return nil, afterID, nil
	}

	authKeyNameMap, err := queryAuthKeyNameMap(ctx, rows)
	if err != nil {
		return nil, afterID, err
	}

	events := make([]RequestStreamEvent, 0, len(rows))
	newestID := afterID
	for _, row := range rows {
		if row.ID > newestID {
			newestID = row.ID
		}

		latencyMs := row.ProxyTimeMs + row.FirstChunkTimeMs + row.ChunkTimeMs
		if latencyMs < 0 {
			latencyMs = 0
		}
		status := strings.TrimSpace(strings.ToLower(row.Status))
		if status == "" {
			status = "unknown"
		}

		authKeyName := "admin"
		if row.AuthKeyID > 0 {
			if name, ok := authKeyNameMap[row.AuthKeyID]; ok && strings.TrimSpace(name) != "" {
				authKeyName = strings.TrimSpace(name)
			} else {
				authKeyName = fmt.Sprintf("authkey-%d", row.AuthKeyID)
			}
		}

		providerName := strings.TrimSpace(row.ProviderName)
		if providerName == "" {
			providerName = "unknown-provider"
		}
		modelName := strings.TrimSpace(row.ModelName)
		if modelName == "" {
			modelName = "unknown-model"
		}

		events = append(events, RequestStreamEvent{
			ID:               row.ID,
			CreatedAt:        row.CreatedAt.Format(time.RFC3339Nano),
			AuthKeyID:        row.AuthKeyID,
			AuthKeyName:      authKeyName,
			ProviderName:     providerName,
			ModelName:        modelName,
			Status:           status,
			LatencyMs:        latencyMs,
			ProxyTimeMs:      row.ProxyTimeMs,
			FirstChunkTimeMs: row.FirstChunkTimeMs,
			ChunkTimeMs:      row.ChunkTimeMs,
			StreamLike:       row.FirstChunkTimeMs > 0 || row.ChunkTimeMs > 0,
		})
	}

	return events, newestID, nil
}

func queryAuthKeyNameMap(ctx context.Context, rows []requestStreamDBRow) (map[uint]string, error) {
	authKeyIDs := make([]uint, 0, len(rows))
	seen := make(map[uint]struct{}, len(rows))
	for _, row := range rows {
		if row.AuthKeyID == 0 {
			continue
		}
		if _, ok := seen[row.AuthKeyID]; ok {
			continue
		}
		seen[row.AuthKeyID] = struct{}{}
		authKeyIDs = append(authKeyIDs, row.AuthKeyID)
	}
	if len(authKeyIDs) == 0 {
		return map[uint]string{}, nil
	}

	authKeys := make([]models.AuthKey, 0, len(authKeyIDs))
	if err := models.DB.WithContext(ctx).
		Model(&models.AuthKey{}).
		Select("id, name").
		Where("id IN ?", authKeyIDs).
		Find(&authKeys).Error; err != nil {
		return nil, err
	}

	keyNameMap := make(map[uint]string, len(authKeys))
	for _, key := range authKeys {
		keyNameMap[key.ID] = key.Name
	}
	return keyNameMap, nil
}

func writeSSEEvent(writer http.ResponseWriter, flusher http.Flusher, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte("event: " + event + "\n")); err != nil {
		return err
	}
	if _, err := writer.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if _, err := writer.Write([]byte("\n\n")); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
