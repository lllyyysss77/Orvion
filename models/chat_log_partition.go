package models

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
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
		"cache_hit_rate",
		"prompt_tokens_details",
		"total_cost",
	}
)

// ChatLogQueryScope 描述跨月表查询范围；为空时查询全部已存在月表。
type ChatLogQueryScope struct {
	StartAt *time.Time
	EndAt   *time.Time
}

// ChatLogUnionQuery 是基于月表拼出的只读查询片段。
type ChatLogUnionQuery struct {
	SQL    string
	Tables []string
}

// ChatLogRef 是月表日志的稳定引用。ID 仅在对应月表内唯一，UUID 跨月表唯一。
type ChatLogRef struct {
	ID        uint
	UUID      string
	TableName string
	CreatedAt time.Time
}

// ChatLogMonthlyTableName 返回指定时间对应的日志月表名，例如 chat_logs_202605。
func ChatLogMonthlyTableName(t time.Time) string {
	year, month, _ := t.Date()
	return fmt.Sprintf("%s%04d%02d", chatLogMonthlyTablePrefix, year, int(month))
}

func ChatLogMonthlyTableNameFromYM(ym string) string {
	return chatLogMonthlyTablePrefix + ym
}

func IsChatLogMonthlyTableName(tableName string) bool {
	return chatLogMonthlyTableRegexp.MatchString(strings.TrimSpace(tableName))
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
func CreateMonthlyChatLog(ctx context.Context, log ChatLog) (ChatLogRef, error) {
	now := time.Now()
	if log.CreatedAt.IsZero() {
		log.CreatedAt = now
	}
	if log.UpdatedAt.IsZero() {
		log.UpdatedAt = now
	}
	tableName, err := EnsureChatLogMonthlyTable(log.CreatedAt)
	if err != nil {
		return ChatLogRef{}, err
	}
	if err := DB.WithContext(ctx).Table(tableName).Create(&log).Error; err != nil {
		return ChatLogRef{}, err
	}
	return ChatLogRef{ID: log.ID, UUID: log.UUID, TableName: tableName, CreatedAt: log.CreatedAt}, nil
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

// UpdateMonthlyChatLogByRef 按稳定引用更新月表日志。
func UpdateMonthlyChatLogByRef(ctx context.Context, ref ChatLogRef, values any) error {
	if ref.UUID == "" && ref.ID == 0 {
		return nil
	}
	tableName := strings.TrimSpace(ref.TableName)
	if tableName == "" {
		tableName = ChatLogMonthlyTableName(ref.CreatedAt)
	}
	if !IsChatLogMonthlyTableName(tableName) {
		return fmt.Errorf("非法日志月表名: %s", tableName)
	}

	query := DB.WithContext(ctx).Table(tableName)
	if ref.UUID != "" {
		query = query.Where("uuid = ?", ref.UUID)
	} else {
		query = query.Where("id = ?", ref.ID)
	}
	result := query.Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 || tableName == ChatLogMonthlyTableName(time.Now()) {
		return nil
	}

	fallbackTable := ChatLogMonthlyTableName(time.Now())
	fallback := DB.WithContext(ctx).Table(fallbackTable)
	if ref.UUID != "" {
		fallback = fallback.Where("uuid = ?", ref.UUID)
	} else {
		fallback = fallback.Where("id = ?", ref.ID)
	}
	return fallback.Updates(values).Error
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

// ListChatLogMonthlyTablesInRange 返回与时间范围相交的月表，避免跨月查询扫全量历史表。
func ListChatLogMonthlyTablesInRange(scope ChatLogQueryScope) ([]string, error) {
	tables, err := ListChatLogMonthlyTables()
	if err != nil {
		return nil, err
	}
	if scope.StartAt == nil && scope.EndAt == nil {
		return tables, nil
	}

	allowed := chatLogMonthSet(scope)
	result := make([]string, 0, len(tables))
	for _, tableName := range tables {
		matches := chatLogMonthlyTableRegexp.FindStringSubmatch(tableName)
		if len(matches) != 2 {
			continue
		}
		if _, ok := allowed[matches[1]]; ok {
			result = append(result, tableName)
		}
	}
	return result, nil
}

// DropEmptyChatLogMonthlyTablesExcept 删除空的日志月表，并保留指定表名。
func DropEmptyChatLogMonthlyTablesExcept(ctx context.Context, keepTables ...string) ([]string, error) {
	tables, err := ListChatLogMonthlyTables()
	if err != nil {
		return nil, err
	}

	keep := make(map[string]struct{}, len(keepTables))
	for _, tableName := range keepTables {
		name := strings.TrimSpace(tableName)
		if name != "" {
			keep[name] = struct{}{}
		}
	}

	dropped := make([]string, 0)
	for _, tableName := range tables {
		if _, ok := keep[tableName]; ok {
			continue
		}

		var count int64
		if err := DB.WithContext(ctx).Table(tableName).Count(&count).Error; err != nil {
			return dropped, err
		}
		if count > 0 {
			continue
		}

		if err := DB.WithContext(ctx).Migrator().DropTable(tableName); err != nil {
			return dropped, err
		}
		chatLogMonthlyTableCache.Delete(tableName)
		dropped = append(dropped, tableName)
	}
	return dropped, nil
}

// ClearChatLogMonthlyTableCacheForTest 清理月表缓存，供测试场景复用。
func ClearChatLogMonthlyTableCacheForTest() {
	chatLogMonthlyTableCache.Range(func(key, _ any) bool {
		chatLogMonthlyTableCache.Delete(key)
		return true
	})
}

// BuildChatLogUnionQuery 生成跨月表查询 SQL；调用方可在外层继续 WHERE/GROUP/ORDER。
func BuildChatLogUnionQuery(scope ChatLogQueryScope, columns string) (ChatLogUnionQuery, error) {
	tables, err := ListChatLogMonthlyTablesInRange(scope)
	if err != nil {
		return ChatLogUnionQuery{}, err
	}
	if len(tables) == 0 {
		return ChatLogUnionQuery{}, nil
	}
	if strings.TrimSpace(columns) == "" {
		columns = ChatLogColumnsSQL()
	}

	selectSQL := make([]string, 0, len(tables))
	for _, tableName := range tables {
		if !IsChatLogMonthlyTableName(tableName) {
			continue
		}
		selectSQL = append(selectSQL, fmt.Sprintf("SELECT %s FROM %s", columns, tableName))
	}
	return ChatLogUnionQuery{
		SQL:    strings.Join(selectSQL, " UNION ALL "),
		Tables: tables,
	}, nil
}

func QueryChatLogCount(ctx context.Context, scope ChatLogQueryScope, whereSQL string, args ...any) (int64, error) {
	union, err := BuildChatLogUnionQuery(scope, "id, created_at, status, auth_key_id, provider_name, name, style")
	if err != nil || union.SQL == "" {
		return 0, err
	}
	sql := "SELECT COUNT(1) AS total FROM (" + union.SQL + ") AS logs"
	if strings.TrimSpace(whereSQL) != "" {
		sql += " WHERE " + whereSQL
	}
	type row struct {
		Total int64 `gorm:"column:total"`
	}
	var total row
	if err := DB.WithContext(ctx).Raw(sql, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Total, nil
}

func QueryChatLogFloatSum(ctx context.Context, scope ChatLogQueryScope, column string, whereSQL string, args ...any) (float64, error) {
	union, err := BuildChatLogUnionQuery(scope, column+", created_at, status, auth_key_id, provider_name, name, style")
	if err != nil || union.SQL == "" {
		return 0, err
	}
	sql := fmt.Sprintf("SELECT COALESCE(SUM(%s),0) AS total FROM (%s) AS logs", column, union.SQL)
	if strings.TrimSpace(whereSQL) != "" {
		sql += " WHERE " + whereSQL
	}
	type row struct {
		Total float64 `gorm:"column:total"`
	}
	var total row
	if err := DB.WithContext(ctx).Raw(sql, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Total, nil
}

// ChatLogColumnsSQL 返回 chat_logs 列清单 SQL 片段。
func ChatLogColumnsSQL() string {
	return strings.Join(chatLogColumnList, ", ")
}

func chatLogMonthSet(scope ChatLogQueryScope) map[string]struct{} {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	if scope.StartAt != nil {
		start = time.Date(scope.StartAt.Year(), scope.StartAt.Month(), 1, 0, 0, 0, 0, scope.StartAt.Location())
	}

	end := now
	if scope.EndAt != nil {
		end = *scope.EndAt
	}
	end = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, end.Location())
	if end.Before(start) {
		end = start
	}

	months := make(map[string]struct{})
	for cursor := start; !cursor.After(end); cursor = cursor.AddDate(0, 1, 0) {
		months[cursor.Format("200601")] = struct{}{}
	}
	return months
}
