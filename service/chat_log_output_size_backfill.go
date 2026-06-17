package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
)

const (
	chatLogOutputSizeBackfillInterval      = 5 * time.Minute
	chatLogOutputSizeBackfillRunTimeout    = 2 * time.Minute
	chatLogOutputSizeBackfillBatchSize     = 500
	chatLogOutputSizeBackfillMaxRowsPerRun = 10000
)

type chatLogOutputSizeBackfillRow struct {
	ID                uint   `gorm:"column:id"`
	UUID              string `gorm:"column:uuid"`
	Name              string `gorm:"column:name"`
	Style             string `gorm:"column:style"`
	Input             string `gorm:"column:input"`
	OutputString      string `gorm:"column:output_string"`
	OutputStringArray string `gorm:"column:output_string_array"`
	PromptTokens      int64  `gorm:"column:prompt_tokens"`
	CompletionTokens  int64  `gorm:"column:completion_tokens"`
	TotalTokens       int64  `gorm:"column:total_tokens"`
}

// StartChatLogOutputSizeBackfill 启动日志输出字节回填任务。
func StartChatLogOutputSizeBackfill(ctx context.Context) {
	slog.Info("日志输出字节回填任务已启动",
		"interval", chatLogOutputSizeBackfillInterval.String(),
		"batch_size", chatLogOutputSizeBackfillBatchSize,
		"max_rows_per_run", chatLogOutputSizeBackfillMaxRowsPerRun,
	)
	pkg.GoSafe("service.chat_log_output_size_backfill", func() { chatLogOutputSizeBackfillLoop(ctx) })
}

func chatLogOutputSizeBackfillLoop(ctx context.Context) {
	runOnce := func() {
		scanCtx, cancel := context.WithTimeout(ctx, chatLogOutputSizeBackfillRunTimeout)
		defer cancel()

		updated, err := BackfillChatLogOutputSizes(scanCtx)
		if err != nil {
			slog.Warn("补充日志输出字节失败", "error", err)
			return
		}
		if updated > 0 {
			slog.Info("日志输出字节回填任务扫描完成", "rows", updated)
		}
	}

	runOnce()
	ticker := time.NewTicker(chatLogOutputSizeBackfillInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

// BackfillChatLogOutputSizes 将成功日志中缺失的响应大小和输出 token 从 chat_io 内容回填到月表。
func BackfillChatLogOutputSizes(ctx context.Context) (int, error) {
	if models.DB == nil {
		return 0, nil
	}

	tables, err := models.ListChatLogMonthlyTables()
	if err != nil {
		return 0, err
	}

	total := 0
	for _, tableName := range tables {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		remaining := chatLogOutputSizeBackfillMaxRowsPerRun - total
		if remaining <= 0 {
			return total, nil
		}

		updated, err := backfillChatLogOutputSizesInTable(ctx, tableName, remaining)
		if err != nil {
			return total, err
		}
		total += updated
	}
	return total, nil
}

func backfillChatLogOutputSizesInTable(ctx context.Context, tableName string, maxRows int) (int, error) {
	if !models.IsChatLogMonthlyTableName(tableName) || maxRows <= 0 {
		return 0, nil
	}

	total := 0
	for total < maxRows {
		limit := chatLogOutputSizeBackfillBatchSize
		if remaining := maxRows - total; remaining < limit {
			limit = remaining
		}

		rows, err := queryChatLogOutputSizeBackfillRows(ctx, tableName, limit)
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}

		changed := 0
		for _, row := range rows {
			size := estimateChatIOOutputSize(row.OutputString, row.OutputStringArray)
			usage := estimateUsageFromChatIO(row)
			if size <= 0 && usage.CompletionTokens <= 0 {
				continue
			}
			ok, err := updateChatLogOutputStats(ctx, tableName, row, size, usage)
			if err != nil {
				return total, err
			}
			if ok {
				total++
				changed++
			}
		}
		if len(rows) < limit || changed == 0 {
			return total, nil
		}
	}
	return total, nil
}

func queryChatLogOutputSizeBackfillRows(ctx context.Context, tableName string, limit int) ([]chatLogOutputSizeBackfillRow, error) {
	if !models.IsChatLogMonthlyTableName(tableName) || limit <= 0 {
		return nil, nil
	}

	sql := fmt.Sprintf(`
SELECT logs.id, logs.uuid, logs.name, logs.style, logs.prompt_tokens, logs.completion_tokens, logs.total_tokens,
       io.input, io.output_string, io.output_string_array
  FROM %s AS logs
  JOIN chat_io AS io
    ON (
      (COALESCE(logs.uuid, '') <> '' AND io.log_uuid = logs.uuid)
      OR (COALESCE(logs.uuid, '') = '' AND COALESCE(io.log_uuid, '') = '' AND io.log_id = logs.id)
    )
 WHERE logs.status = ?
   AND (COALESCE(logs.size, 0) = 0 OR COALESCE(logs.completion_tokens, 0) = 0)
   AND TRIM(COALESCE(io.input, '')) <> ''
   AND (
     LENGTH(COALESCE(io.output_string, '')) > 0
     OR LENGTH(COALESCE(io.output_string_array, '')) > 0
   )
   AND io.id = (
     SELECT MAX(io2.id)
       FROM chat_io AS io2
      WHERE (
        (COALESCE(logs.uuid, '') <> '' AND io2.log_uuid = logs.uuid)
        OR (COALESCE(logs.uuid, '') = '' AND COALESCE(io2.log_uuid, '') = '' AND io2.log_id = logs.id)
      )
        AND TRIM(COALESCE(io2.input, '')) <> ''
        AND (
          LENGTH(COALESCE(io2.output_string, '')) > 0
          OR LENGTH(COALESCE(io2.output_string_array, '')) > 0
        )
   )
 ORDER BY logs.created_at ASC, logs.id ASC
 LIMIT ?`, tableName)

	rows := make([]chatLogOutputSizeBackfillRow, 0, limit)
	if err := models.DB.WithContext(ctx).Raw(sql, "success", limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func updateChatLogOutputStats(ctx context.Context, tableName string, row chatLogOutputSizeBackfillRow, size int, usage models.Usage) (bool, error) {
	if !models.IsChatLogMonthlyTableName(tableName) {
		return false, nil
	}

	updates := make(map[string]any)
	if size > 0 {
		updates["size"] = size
	}
	if usage.CompletionTokens > 0 {
		updates["completion_tokens"] = usage.CompletionTokens
		totalTokens := row.TotalTokens
		if totalTokens <= row.PromptTokens {
			totalTokens = row.PromptTokens + usage.CompletionTokens
		}
		updates["total_tokens"] = totalTokens
	}
	if len(updates) == 0 {
		return false, nil
	}
	updates["updated_at"] = time.Now()

	query := models.DB.WithContext(ctx).
		Table(tableName).
		Where("status = ?", "success").
		Where("(COALESCE(size, 0) = 0 OR COALESCE(completion_tokens, 0) = 0)")
	if strings.TrimSpace(row.UUID) != "" {
		query = query.Where("uuid = ?", row.UUID)
	} else {
		query = query.Where("id = ?", row.ID)
	}

	result := query.Updates(updates)
	return result.RowsAffected > 0, result.Error
}

func estimateChatIOOutputSize(outputString string, outputStringArray string) int {
	if outputString != "" {
		return len(outputString)
	}

	rawArray := strings.TrimSpace(outputStringArray)
	if rawArray == "" {
		return 0
	}

	var items []string
	if err := json.Unmarshal([]byte(rawArray), &items); err != nil {
		return len(rawArray)
	}

	size := 0
	for _, item := range items {
		size += len(item)
	}
	return size
}

func estimateUsageFromChatIO(row chatLogOutputSizeBackfillRow) models.Usage {
	if row.CompletionTokens > 0 {
		return models.Usage{}
	}
	output := models.OutputUnion{}
	if strings.TrimSpace(row.OutputString) != "" {
		output.OfString = row.OutputString
	} else if strings.TrimSpace(row.OutputStringArray) != "" {
		_ = json.Unmarshal([]byte(row.OutputStringArray), &output.OfStringArray)
		if len(output.OfStringArray) == 0 {
			output.OfString = row.OutputStringArray
		}
	}
	if output.OfString == "" && len(output.OfStringArray) == 0 {
		return models.Usage{}
	}
	return estimateUsageFromIO(row.Style, row.Name, []byte(row.Input), &output)
}
