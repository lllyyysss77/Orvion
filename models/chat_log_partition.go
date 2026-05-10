package models

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	chatLogMonthlyTablePrefix = "chat_logs_"
)

var (
	chatLogMonthlyTableRegexp = regexp.MustCompile(`^chat_logs_(\d{6})$`)
	chatLogMonthlyTableCache  sync.Map
	chatLogColumnList         = []string{
		"id",
		"created_at",
		"updated_at",
		"deleted_at",
		"uuid",
		"name",
		"provider_model",
		"provider_name",
		"model_with_provider_id",
		"status",
		"style",
		"request_path",
		"user_agent",
		"remote_ip",
		"auth_key_id",
		"chat_io",
		"error",
		"retry",
		"proxy_time_ms",
		"first_chunk_time_ms",
		"chunk_time_ms",
		"tps",
		"size",
		"prompt_tokens",
		"completion_tokens",
		"total_tokens",
		"cached_tokens",
		"prompt_tokens_details",
		"total_cost",
	}
)

// ChatLogMonthlyTableName 返回指定时间对应的日志月表名，例如 chat_logs_202605。
func ChatLogMonthlyTableName(t time.Time) string {
	year, month, _ := t.Date()
	return fmt.Sprintf("%s%04d%02d", chatLogMonthlyTablePrefix, year, int(month))
}

// EnsureChatLogMonthlyTable 确保指定月份日志分表存在。
func EnsureChatLogMonthlyTable(t time.Time) (string, error) {
	tableName := ChatLogMonthlyTableName(t)
	if _, ok := chatLogMonthlyTableCache.Load(tableName); ok {
		return tableName, nil
	}
	if err := DB.Table(tableName).AutoMigrate(&ChatLog{}); err != nil {
		return "", err
	}
	chatLogMonthlyTableCache.Store(tableName, struct{}{})
	return tableName, nil
}

// CreateMonthlyChatLog 在对应月表写入日志记录。
func CreateMonthlyChatLog(ctx context.Context, log ChatLog) error {
	tableName, err := EnsureChatLogMonthlyTable(log.CreatedAt)
	if err != nil {
		return err
	}
	return DB.WithContext(ctx).Table(tableName).Create(&log).Error
}

// UpdateMonthlyChatLogByTime 按月份和ID更新月表日志。
func UpdateMonthlyChatLogByTime(ctx context.Context, createdAt time.Time, id uint, values any) error {
	if id == 0 || createdAt.IsZero() {
		return nil
	}
	tableName := ChatLogMonthlyTableName(createdAt)
	result := DB.WithContext(ctx).Table(tableName).Where("id = ?", id).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	fallbackTable := ChatLogMonthlyTableName(time.Now())
	if fallbackTable == tableName {
		return nil
	}
	if err := DB.WithContext(ctx).Table(fallbackTable).Where("id = ?", id).Updates(values).Error; err != nil {
		return err
	}
	return nil
}

// ListChatLogMonthlyTables 返回全部日志月表，按表名升序。
func ListChatLogMonthlyTables() ([]string, error) {
	tables, err := DB.Migrator().GetTables()
	if err != nil {
		return nil, err
	}

	monthlyTables := make([]string, 0)
	for _, table := range tables {
		name := strings.TrimSpace(table)
		if chatLogMonthlyTableRegexp.MatchString(name) {
			monthlyTables = append(monthlyTables, name)
			chatLogMonthlyTableCache.Store(name, struct{}{})
		}
	}
	sort.Strings(monthlyTables)
	return monthlyTables, nil
}

// ChatLogColumnsSQL 返回 chat_logs 列清单 SQL 片段。
func ChatLogColumnsSQL() string {
	return strings.Join(chatLogColumnList, ", ")
}

// BackfillChatLogsToMonthly 将主表历史日志回填到按月分表（幂等）。
func BackfillChatLogsToMonthly(ctx context.Context) (int64, error) {
	months, err := listChatLogYearMonths(ctx)
	if err != nil {
		return 0, err
	}
	if len(months) == 0 {
		return 0, nil
	}

	columns := ChatLogColumnsSQL()
	totalAffected := int64(0)
	for _, ym := range months {
		if len(ym) != 6 {
			continue
		}
		year, month := ym[:4], ym[4:]
		tableName := fmt.Sprintf("%s%s%s", chatLogMonthlyTablePrefix, year, month)
		if !chatLogMonthlyTableRegexp.MatchString(tableName) {
			continue
		}
		if _, ok := chatLogMonthlyTableCache.Load(tableName); !ok {
			if err := DB.Table(tableName).AutoMigrate(&ChatLog{}); err != nil {
				return totalAffected, err
			}
			chatLogMonthlyTableCache.Store(tableName, struct{}{})
		}

		result, execErr := backfillOneMonth(ctx, tableName, columns, ym)
		if execErr != nil {
			return totalAffected, execErr
		}
		totalAffected += result.RowsAffected
	}
	return totalAffected, nil
}

func listChatLogYearMonths(ctx context.Context) ([]string, error) {
	type row struct {
		YM string `gorm:"column:ym"`
	}
	rows := make([]row, 0)
	sql := "SELECT DISTINCT strftime('%Y%m', created_at, 'localtime') AS ym FROM chat_logs WHERE created_at IS NOT NULL ORDER BY ym"
	if DB.Dialector.Name() == "postgres" {
		sql = "SELECT DISTINCT TO_CHAR(created_at, 'YYYYMM') AS ym FROM chat_logs WHERE created_at IS NOT NULL ORDER BY ym"
	}
	if err := DB.WithContext(ctx).Raw(sql).Scan(&rows).Error; err != nil {
		return nil, err
	}
	months := make([]string, 0, len(rows))
	for _, item := range rows {
		ym := strings.TrimSpace(item.YM)
		if len(ym) == 6 {
			months = append(months, ym)
		}
	}
	return months, nil
}

func backfillOneMonth(ctx context.Context, tableName string, columns string, ym string) (*gorm.DB, error) {
	switch DB.Dialector.Name() {
	case "postgres":
		sql := fmt.Sprintf(
			"INSERT INTO %s (%s) SELECT %s FROM chat_logs WHERE TO_CHAR(created_at, 'YYYYMM') = ? ON CONFLICT (id) DO NOTHING",
			tableName,
			columns,
			columns,
		)
		result := DB.WithContext(ctx).Exec(sql, ym)
		return result, result.Error
	default:
		sql := fmt.Sprintf(
			"INSERT OR IGNORE INTO %s (%s) SELECT %s FROM chat_logs WHERE strftime('%%Y%%m', created_at, 'localtime') = ?",
			tableName,
			columns,
			columns,
		)
		result := DB.WithContext(ctx).Exec(sql, ym)
		return result, result.Error
	}
}
