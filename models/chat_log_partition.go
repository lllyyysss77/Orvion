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
	chatLogMonthlyTablesMu    sync.RWMutex
	chatLogMonthlyTables      []string
	chatLogMonthlyTablesReady bool
	chatLogMonthlyIndexSpecs  = []chatLogMonthlyIndexSpec{
		{Name: "created_id", Columns: []string{"created_at", "id"}},
		{Name: "status_created", Columns: []string{"status", "created_at"}},
		{Name: "auth_created", Columns: []string{"auth_key_id", "created_at"}},
		{Name: "model_created", Columns: []string{"name", "created_at"}},
		{Name: "provider_created", Columns: []string{"provider_name", "created_at"}},
		{Name: "mwp_status", Columns: []string{"model_with_provider_id", "status"}},
	}
	chatLogColumnList = []string{
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
		"traffic_bytes",
		"prompt_tokens",
		"completion_tokens",
		"total_tokens",
		"cached_tokens",
		"cache_hit_rate",
		"prompt_tokens_details",
		"total_cost",
	}
	chatLogListCountColumnList = []string{
		"id",
		"created_at",
		"uuid",
		"name",
		"provider_model",
		"provider_name",
		"model_with_provider_id",
		"status",
		"style",
		"auth_key_id",
	}
)

type chatLogMonthlyIndexSpec struct {
	Name    string
	Columns []string
}

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

type CountRow struct {
	Total int64 `gorm:"column:total"`
}

type ChatLogMetricsAgg struct {
	Reqs    int64   `gorm:"column:reqs"`
	Success int64   `gorm:"column:success"`
	Prompt  int64   `gorm:"column:prompt"`
	Output  int64   `gorm:"column:completion"`
	Tokens  int64   `gorm:"column:tokens"`
	Amount  float64 `gorm:"column:amount"`
	TimeMs  int64   `gorm:"column:time_ms"`
}

type ChatLogHourAmountRow struct {
	HourBucket int     `gorm:"column:hour_bucket"`
	Requests   int64   `gorm:"column:requests"`
	Amount     float64 `gorm:"column:amount"`
}

type ChatLogModelUsageRow struct {
	Model       string  `gorm:"column:model"`
	TotalTokens int64   `gorm:"column:total_tokens"`
	TotalCost   float64 `gorm:"column:total_cost"`
}

type ChatLogDailyModelCostRow struct {
	DayBucket string  `gorm:"column:day_bucket"`
	Model     string  `gorm:"column:model"`
	Amount    float64 `gorm:"column:amount"`
}

type ChatLogModelCountRow struct {
	Model string `gorm:"column:model"`
	Calls int64  `gorm:"column:calls"`
}

type ChatLogAuthKeyCountRow struct {
	AuthKeyID uint  `gorm:"column:auth_key_id"`
	Calls     int64 `gorm:"column:calls"`
}

type ChatLogProviderUsageRow struct {
	ProviderName string `gorm:"column:provider_name"`
	UsageCount   int64  `gorm:"column:usage_count"`
}

type ChatLogProxyTrafficRow struct {
	ProxyID      uint  `gorm:"column:proxy_id"`
	TrafficBytes int64 `gorm:"column:traffic_bytes"`
}

type ChatLogProviderSuccessStat struct {
	ProviderName string `gorm:"column:provider_name"`
	TotalCount   int64  `gorm:"column:total_count"`
	SuccessCount int64  `gorm:"column:success_count"`
}

type ChatLogAuthKeySummaryAgg struct {
	TotalRequests   int64   `gorm:"column:total_requests"`
	SuccessRequests int64   `gorm:"column:success_requests"`
	Prompt          int64   `gorm:"column:prompt"`
	Completion      int64   `gorm:"column:completion"`
	Total           int64   `gorm:"column:total"`
	TotalCost       float64 `gorm:"column:total_cost"`
	TotalTime       int64   `gorm:"column:total_time"`
}

type ChatLogAuthKeyTokenAgg struct {
	Model      string `gorm:"column:model"`
	Completion int64  `gorm:"column:completion"`
}

type ChatLogDailyUsageSummary struct {
	TotalRequests   int64
	SuccessRequests int64
	TotalCost       float64
	TopModelName    string
	TopModelReqs    int64
	TopAuthKeyID    uint
	TopAuthKeyReqs  int64
	SlowestRequest  *ChatLogSlowRequestRow `gorm:"-"`
}

type ChatLogSlowRequestRow struct {
	ID        uint      `gorm:"column:id"`
	Name      string    `gorm:"column:name"`
	Provider  string    `gorm:"column:provider_name"`
	Status    string    `gorm:"column:status"`
	CreatedAt time.Time `gorm:"column:created_at"`
	LatencyMs int       `gorm:"column:latency_ms"`
}

type ChatLogHealthRow struct {
	Name          string    `gorm:"column:name"`
	ProviderName  string    `gorm:"column:provider_name"`
	ProviderModel string    `gorm:"column:provider_model"`
	Status        string    `gorm:"column:status"`
	Error         string    `gorm:"column:error"`
	ProxyTimeMs   int       `gorm:"column:proxy_time_ms"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

type ChatLogModelProviderSuccessStat struct {
	ModelWithProviderID uint  `gorm:"column:model_with_provider_id"`
	TotalCount          int64 `gorm:"column:total_count"`
	SuccessCount        int64 `gorm:"column:success_count"`
}

type ChatLogOutputStatsBackfillRow struct {
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

type ChatLogCleanupRefRow struct {
	ID        uint      `gorm:"column:id"`
	UUID      string    `gorm:"column:uuid"`
	CreatedAt time.Time `gorm:"column:created_at"`
	TableName string    `gorm:"column:table_name"`
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
	if err := ensureChatLogMonthlyTableSchema(DB, tableName); err != nil {
		return "", err
	}
	rememberChatLogMonthlyTable(tableName)
	return tableName, nil
}

// EnsureAllChatLogMonthlyTableIndexes 为历史日志月表补齐索引。
func EnsureAllChatLogMonthlyTableIndexes() error {
	tables, err := ListChatLogMonthlyTables()
	if err != nil {
		return err
	}
	for _, tableName := range tables {
		if err := ensureChatLogMonthlyTableSchema(DB, tableName); err != nil {
			return err
		}
		rememberChatLogMonthlyTable(tableName)
	}
	return nil
}

// EnsureChatLogMonthlyTableSchemaForDB 为指定连接上的日志月表补齐表结构与索引。
func EnsureChatLogMonthlyTableSchemaForDB(db *gorm.DB, tableName string) error {
	return ensureChatLogMonthlyTableSchema(db, tableName)
}

func ensureChatLogMonthlyTableSchema(db *gorm.DB, tableName string) error {
	if db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if !IsChatLogMonthlyTableName(tableName) {
		return fmt.Errorf("非法日志月表名: %s", tableName)
	}
	if err := db.Table(tableName).AutoMigrate(&ChatLog{}); err != nil {
		return err
	}
	return ensureChatLogMonthlyTableIndexes(db, tableName)
}

func ensureChatLogMonthlyTableIndexes(db *gorm.DB, tableName string) error {
	for _, spec := range chatLogMonthlyIndexSpecs {
		indexName := chatLogMonthlyIndexName(tableName, spec.Name)
		if db.Migrator().HasIndex(tableName, indexName) {
			continue
		}
		columns := make([]string, 0, len(spec.Columns))
		for _, column := range spec.Columns {
			columns = append(columns, quoteIdentifierForDB(db, column))
		}
		sql := fmt.Sprintf(
			"CREATE INDEX %s ON %s (%s)",
			quoteIdentifierForDB(db, indexName),
			quoteIdentifierForDB(db, tableName),
			strings.Join(columns, ", "),
		)
		if err := db.Exec(sql).Error; err != nil {
			return err
		}
	}
	return nil
}

func chatLogMonthlyIndexName(tableName string, suffix string) string {
	name := "idx_" + strings.TrimSpace(tableName) + "_" + strings.TrimSpace(suffix)
	name = strings.NewReplacer("`", "_", `"`, "_", ".", "_", "-", "_").Replace(name)
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func quoteIdentifierForDB(db *gorm.DB, name string) string {
	if db != nil && db.Dialector != nil && strings.EqualFold(strings.TrimSpace(db.Dialector.Name()), string(DatabaseDriverMySQL)) {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
	chatLogMonthlyTablesMu.RLock()
	if chatLogMonthlyTablesReady {
		cached := append([]string(nil), chatLogMonthlyTables...)
		chatLogMonthlyTablesMu.RUnlock()
		return cached, nil
	}
	chatLogMonthlyTablesMu.RUnlock()

	chatLogMonthlyTablesMu.Lock()
	defer chatLogMonthlyTablesMu.Unlock()
	if chatLogMonthlyTablesReady {
		return append([]string(nil), chatLogMonthlyTables...), nil
	}

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
	chatLogMonthlyTables = monthlyTables
	chatLogMonthlyTablesReady = true
	return append([]string(nil), chatLogMonthlyTables...), nil
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
		forgetChatLogMonthlyTable(tableName)
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
	chatLogMonthlyTablesMu.Lock()
	chatLogMonthlyTables = nil
	chatLogMonthlyTablesReady = false
	chatLogMonthlyTablesMu.Unlock()
}

func rememberChatLogMonthlyTable(tableName string) {
	tableName = strings.TrimSpace(tableName)
	if !IsChatLogMonthlyTableName(tableName) {
		return
	}
	chatLogMonthlyTableCache.Store(tableName, struct{}{})

	chatLogMonthlyTablesMu.Lock()
	defer chatLogMonthlyTablesMu.Unlock()
	if !chatLogMonthlyTablesReady {
		return
	}
	index := sort.SearchStrings(chatLogMonthlyTables, tableName)
	if index < len(chatLogMonthlyTables) && chatLogMonthlyTables[index] == tableName {
		return
	}
	chatLogMonthlyTables = append(chatLogMonthlyTables, "")
	copy(chatLogMonthlyTables[index+1:], chatLogMonthlyTables[index:])
	chatLogMonthlyTables[index] = tableName
}

func forgetChatLogMonthlyTable(tableName string) {
	tableName = strings.TrimSpace(tableName)
	chatLogMonthlyTableCache.Delete(tableName)

	chatLogMonthlyTablesMu.Lock()
	defer chatLogMonthlyTablesMu.Unlock()
	if !chatLogMonthlyTablesReady {
		return
	}
	index := sort.SearchStrings(chatLogMonthlyTables, tableName)
	if index >= len(chatLogMonthlyTables) || chatLogMonthlyTables[index] != tableName {
		return
	}
	chatLogMonthlyTables = append(chatLogMonthlyTables[:index], chatLogMonthlyTables[index+1:]...)
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
		selectSQL = append(selectSQL, fmt.Sprintf("SELECT %s FROM %s", columns, QuoteTableName(tableName)))
	}
	return ChatLogUnionQuery{
		SQL:    strings.Join(selectSQL, " UNION ALL "),
		Tables: tables,
	}, nil
}

func QueryChatLogCount(ctx context.Context, scope ChatLogQueryScope, whereSQL string, args ...any) (int64, error) {
	union, err := BuildChatLogUnionQuery(scope, "id, created_at, status, auth_key_id, provider_name, name, style, model_with_provider_id")
	if err != nil || union.SQL == "" {
		return 0, err
	}
	sql := "SELECT COUNT(1) AS total FROM (" + union.SQL + ") AS logs" + normalizeWhereSQL(whereSQL)
	type row struct {
		Total int64 `gorm:"column:total"`
	}
	var total row
	if err := DB.WithContext(ctx).Raw(sql, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Total, nil
}

func QueryChatLogExists(ctx context.Context, scope ChatLogQueryScope, columns string, whereSQL string, args ...any) (bool, error) {
	total, err := QueryChatLogCountWithColumns(ctx, scope, columns, whereSQL, args...)
	return total > 0, err
}

func QueryChatLogCountWithColumns(ctx context.Context, scope ChatLogQueryScope, columns string, whereSQL string, args ...any) (int64, error) {
	union, err := BuildChatLogUnionQuery(scope, columns)
	if err != nil || union.SQL == "" {
		return 0, err
	}
	sql := "SELECT COUNT(1) AS total FROM (" + union.SQL + ") AS logs" + normalizeWhereSQL(whereSQL)
	var total CountRow
	if err := DB.WithContext(ctx).Raw(sql, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Total, nil
}

func QueryChatLogsPage(ctx context.Context, scope ChatLogQueryScope, columns string, whereSQL string, orderSQL string, limit int, offset int, args ...any) ([]ChatLog, int64, error) {
	return queryChatLogsPage(ctx, scope, columns, columns, whereSQL, orderSQL, limit, offset, args...)
}

// QueryChatLogListPage 为请求日志列表查询分页数据。计数查询只读取筛选所需列，
// 避免跨月表 COUNT 为完整日志记录构建宽行。
func QueryChatLogListPage(ctx context.Context, scope ChatLogQueryScope, columns string, whereSQL string, orderSQL string, limit int, offset int, args ...any) ([]ChatLog, int64, error) {
	return queryChatLogsPage(ctx, scope, columns, ChatLogListCountColumnsSQL(), whereSQL, orderSQL, limit, offset, args...)
}

func queryChatLogsPage(ctx context.Context, scope ChatLogQueryScope, columns string, countColumns string, whereSQL string, orderSQL string, limit int, offset int, args ...any) ([]ChatLog, int64, error) {
	if strings.TrimSpace(columns) == "" {
		columns = ChatLogColumnsSQL()
	}
	if strings.TrimSpace(countColumns) == "" {
		countColumns = columns
	}
	union, err := BuildChatLogUnionQuery(scope, columns)
	if err != nil || union.SQL == "" {
		return []ChatLog{}, 0, err
	}

	sqlWhere := normalizeWhereSQL(whereSQL)

	countUnion, err := BuildChatLogUnionQuery(scope, countColumns)
	if err != nil || countUnion.SQL == "" {
		return []ChatLog{}, 0, err
	}
	var total CountRow
	countSQL := "SELECT COUNT(1) AS total FROM (" + countUnion.SQL + ") AS logs" + sqlWhere
	if err := DB.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	if strings.TrimSpace(orderSQL) == "" {
		orderSQL = "created_at DESC, id DESC"
	}
	pageArgs := append(append(make([]any, 0, len(args)+2), args...), limit, offset)
	pageSQL := "SELECT " + columns + " FROM (" + union.SQL + ") AS logs" + sqlWhere + " ORDER BY " + orderSQL + " LIMIT ? OFFSET ?"
	logs := make([]ChatLog, 0, limit)
	if err := DB.WithContext(ctx).Raw(pageSQL, pageArgs...).Scan(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total.Total, nil
}

func QueryChatLogFloatSum(ctx context.Context, scope ChatLogQueryScope, column string, whereSQL string, args ...any) (float64, error) {
	union, err := BuildChatLogUnionQuery(scope, column+", created_at, status, auth_key_id, provider_name, name, style")
	if err != nil || union.SQL == "" {
		return 0, err
	}
	sql := fmt.Sprintf("SELECT COALESCE(SUM(%s),0) AS total FROM (%s) AS logs", column, union.SQL)
	sql += normalizeWhereSQL(whereSQL)
	type row struct {
		Total float64 `gorm:"column:total"`
	}
	var total row
	if err := DB.WithContext(ctx).Raw(sql, args...).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total.Total, nil
}

func QueryChatLogMetricsAgg(ctx context.Context, scope ChatLogQueryScope, whereSQL string, args ...any) (ChatLogMetricsAgg, error) {
	union, err := BuildChatLogUnionQuery(scope, "id, created_at, status, prompt_tokens, completion_tokens, total_tokens, total_cost, proxy_time_ms")
	if err != nil || union.SQL == "" {
		return ChatLogMetricsAgg{}, err
	}

	sql := `SELECT COUNT(1) AS reqs,
	               COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success,
	               COALESCE(SUM(prompt_tokens),0) AS prompt,
	               COALESCE(SUM(completion_tokens),0) AS completion,
	               COALESCE(SUM(total_tokens),0) AS tokens,
		               COALESCE(SUM(total_cost),0) AS amount,
		               COALESCE(SUM(proxy_time_ms),0) AS time_ms
		          FROM (` + union.SQL + `) AS logs`
	sql += normalizeWhereSQL(whereSQL)

	var agg ChatLogMetricsAgg
	if err := DB.WithContext(ctx).Raw(sql, args...).Scan(&agg).Error; err != nil {
		return ChatLogMetricsAgg{}, err
	}
	return agg, nil
}

func QueryChatLogModelCounts(ctx context.Context, scope ChatLogQueryScope) ([]ChatLogModelCountRow, error) {
	union, err := BuildChatLogUnionQuery(scope, "name")
	if err != nil || union.SQL == "" {
		return []ChatLogModelCountRow{}, err
	}

	rows := make([]ChatLogModelCountRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT name AS model, COUNT(*) AS calls
		   FROM (` + union.SQL + `) AS logs
		  GROUP BY name
		  ORDER BY calls DESC`,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogAuthKeyCounts(ctx context.Context, scope ChatLogQueryScope) ([]ChatLogAuthKeyCountRow, error) {
	union, err := BuildChatLogUnionQuery(scope, "auth_key_id")
	if err != nil || union.SQL == "" {
		return []ChatLogAuthKeyCountRow{}, err
	}

	rows := make([]ChatLogAuthKeyCountRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT auth_key_id, COUNT(*) AS calls
		   FROM (` + union.SQL + `) AS logs
		  GROUP BY auth_key_id
		  ORDER BY calls DESC`,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogProviderUsage(ctx context.Context, scope ChatLogQueryScope, whereSQL string, args ...any) ([]ChatLogProviderUsageRow, error) {
	union, err := BuildChatLogUnionQuery(scope, "created_at, provider_name")
	if err != nil || union.SQL == "" {
		return []ChatLogProviderUsageRow{}, err
	}

	rows := make([]ChatLogProviderUsageRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT provider_name, COUNT(*) AS usage_count
		   FROM (`+union.SQL+`) AS logs`+normalizeWhereSQL(whereSQL)+`
		  GROUP BY provider_name`,
		args...,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogProxyTraffic(ctx context.Context, startAt time.Time, endAt time.Time) ([]ChatLogProxyTrafficRow, error) {
	scope := ChatLogQueryScope{StartAt: &startAt, EndAt: &endAt}
	union, err := BuildChatLogUnionQuery(scope, "created_at, model_with_provider_id, traffic_bytes")
	if err != nil || union.SQL == "" {
		return []ChatLogProxyTrafficRow{}, err
	}
	rows := make([]ChatLogProxyTrafficRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT providers.proxy_id, COALESCE(SUM(logs.traffic_bytes), 0) AS traffic_bytes
		   FROM (`+union.SQL+`) AS logs
		   JOIN model_with_providers ON model_with_providers.id = logs.model_with_provider_id
		   JOIN providers ON providers.id = model_with_providers.provider_id
		  WHERE logs.created_at >= ?
		    AND logs.created_at < ?
		    AND providers.proxy_id > 0
		  GROUP BY providers.proxy_id`,
		startAt,
		endAt,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogProviderSuccessStats(ctx context.Context, startAt time.Time, endAt time.Time, providerNames []string) ([]ChatLogProviderSuccessStat, error) {
	if len(providerNames) == 0 {
		return []ChatLogProviderSuccessStat{}, nil
	}
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{StartAt: &startAt, EndAt: &endAt}, "created_at, provider_name, status")
	if err != nil || union.SQL == "" {
		return []ChatLogProviderSuccessStat{}, err
	}

	stats := make([]ChatLogProviderSuccessStat, 0, len(providerNames))
	if err := DB.WithContext(ctx).Raw(
		`SELECT provider_name,
		        COUNT(1) AS total_count,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success_count
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ?
		    AND created_at < ?
		    AND provider_name IN ?
		  GROUP BY provider_name`,
		startAt,
		endAt,
		providerNames,
	).Scan(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

func QueryChatLogAuthKeySummary(ctx context.Context, authKeyID uint) (ChatLogAuthKeySummaryAgg, error) {
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{}, "auth_key_id, status, prompt_tokens, completion_tokens, total_tokens, total_cost, proxy_time_ms")
	if err != nil || union.SQL == "" {
		return ChatLogAuthKeySummaryAgg{}, err
	}

	var agg ChatLogAuthKeySummaryAgg
	if err := DB.WithContext(ctx).Raw(
		`SELECT COUNT(1) AS total_requests,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success_requests,
		        COALESCE(SUM(prompt_tokens),0) AS prompt,
		        COALESCE(SUM(completion_tokens),0) AS completion,
		        COALESCE(SUM(total_tokens),0) AS total,
		        COALESCE(SUM(total_cost),0) AS total_cost,
		        COALESCE(SUM(proxy_time_ms),0) AS total_time
		   FROM (`+union.SQL+`) AS logs
		  WHERE auth_key_id = ?`,
		authKeyID,
	).Scan(&agg).Error; err != nil {
		return ChatLogAuthKeySummaryAgg{}, err
	}
	return agg, nil
}

func QueryChatLogAuthKeyCompletionByModel(ctx context.Context, authKeyID uint) ([]ChatLogAuthKeyTokenAgg, error) {
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{}, "auth_key_id, name, completion_tokens")
	if err != nil || union.SQL == "" {
		return []ChatLogAuthKeyTokenAgg{}, err
	}

	rows := make([]ChatLogAuthKeyTokenAgg, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT LOWER(name) AS model, COALESCE(SUM(completion_tokens),0) AS completion
		   FROM (`+union.SQL+`) AS logs
		  WHERE auth_key_id = ?
		  GROUP BY LOWER(name)`,
		authKeyID,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogDailyUsageSummary(ctx context.Context, startAt time.Time, endAt time.Time) (ChatLogDailyUsageSummary, error) {
	union, err := BuildChatLogUnionQuery(
		ChatLogQueryScope{StartAt: &startAt, EndAt: &endAt},
		"id, created_at, status, total_cost, name, auth_key_id, provider_name, proxy_time_ms, first_chunk_time_ms, chunk_time_ms",
	)
	if err != nil || union.SQL == "" {
		return ChatLogDailyUsageSummary{}, err
	}

	baseSQL := "FROM (" + union.SQL + ") AS logs WHERE created_at >= ? AND created_at < ?"
	var summary ChatLogDailyUsageSummary
	if err := DB.WithContext(ctx).Raw(
		`SELECT COUNT(1) AS total_requests,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success_requests,
		        COALESCE(SUM(total_cost),0) AS total_cost `+baseSQL,
		startAt,
		endAt,
	).Scan(&summary).Error; err != nil {
		return ChatLogDailyUsageSummary{}, err
	}

	type topModelRow struct {
		Name     string `gorm:"column:name"`
		ReqCount int64  `gorm:"column:req_count"`
	}
	var topModel topModelRow
	if err := DB.WithContext(ctx).Raw(
		`SELECT name, COUNT(1) AS req_count `+baseSQL+`
		  AND status = ?
		  AND name <> ?
		  GROUP BY name
		  ORDER BY req_count DESC, name ASC
		  LIMIT 1`,
		startAt,
		endAt,
		"success",
		"",
	).Scan(&topModel).Error; err != nil {
		return ChatLogDailyUsageSummary{}, err
	}
	summary.TopModelName = strings.TrimSpace(topModel.Name)
	summary.TopModelReqs = topModel.ReqCount

	type topAuthKeyRow struct {
		AuthKeyID uint  `gorm:"column:auth_key_id"`
		ReqCount  int64 `gorm:"column:req_count"`
	}
	var topAuthKey topAuthKeyRow
	if err := DB.WithContext(ctx).Raw(
		`SELECT auth_key_id, COUNT(1) AS req_count `+baseSQL+`
		  AND status = ?
		  AND auth_key_id > ?
		  GROUP BY auth_key_id
		  ORDER BY req_count DESC, auth_key_id ASC
		  LIMIT 1`,
		startAt,
		endAt,
		"success",
		0,
	).Scan(&topAuthKey).Error; err != nil {
		return ChatLogDailyUsageSummary{}, err
	}
	summary.TopAuthKeyID = topAuthKey.AuthKeyID
	summary.TopAuthKeyReqs = topAuthKey.ReqCount

	var slowest ChatLogSlowRequestRow
	if err := DB.WithContext(ctx).Raw(
		`SELECT id, name, provider_name, status, created_at,
		        (COALESCE(proxy_time_ms,0) + COALESCE(first_chunk_time_ms,0) + COALESCE(chunk_time_ms,0)) AS latency_ms `+baseSQL+`
		  ORDER BY latency_ms DESC, id DESC
		  LIMIT 1`,
		startAt,
		endAt,
	).Scan(&slowest).Error; err != nil {
		return ChatLogDailyUsageSummary{}, err
	}
	if slowest.ID > 0 {
		summary.SlowestRequest = &slowest
	}
	return summary, nil
}

func QueryChatLogHealthRows(ctx context.Context, startAt time.Time, endAt time.Time, providerNames []string, modelNames []string, providerModels []string) ([]ChatLogHealthRow, error) {
	if len(providerNames) == 0 || len(modelNames) == 0 || len(providerModels) == 0 {
		return []ChatLogHealthRow{}, nil
	}
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{StartAt: &startAt, EndAt: &endAt}, "name, provider_name, provider_model, status, error, proxy_time_ms, created_at")
	if err != nil || union.SQL == "" {
		return []ChatLogHealthRow{}, err
	}

	rows := make([]ChatLogHealthRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT name, provider_name, provider_model, status, error, proxy_time_ms, created_at
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ?
		    AND created_at < ?
		    AND provider_name IN ?
		    AND name IN ?
		    AND provider_model IN ?`,
		startAt,
		endAt,
		providerNames,
		modelNames,
		providerModels,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogModelProviderSuccessStats(ctx context.Context, ids []uint) ([]ChatLogModelProviderSuccessStat, error) {
	if len(ids) == 0 {
		return []ChatLogModelProviderSuccessStat{}, nil
	}
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{}, "model_with_provider_id, status")
	if err != nil || union.SQL == "" {
		return []ChatLogModelProviderSuccessStat{}, err
	}

	stats := make([]ChatLogModelProviderSuccessStat, 0, len(ids))
	if err := DB.WithContext(ctx).Raw(
		`SELECT model_with_provider_id AS model_with_provider_id,
		        COUNT(1) AS total_count,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success_count
		   FROM (`+union.SQL+`) AS logs
		  WHERE model_with_provider_id IN ?
		  GROUP BY model_with_provider_id`,
		ids,
	).Scan(&stats).Error; err != nil {
		return nil, err
	}
	return stats, nil
}

func QueryChatLogDistinctUserAgents(ctx context.Context) ([]string, error) {
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{}, "user_agent")
	if err != nil || union.SQL == "" {
		return []string{}, err
	}

	userAgents := make([]string, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT DISTINCT user_agent
		   FROM (` + union.SQL + `) AS logs
		  WHERE user_agent IS NOT NULL AND user_agent != ''
		  ORDER BY user_agent ASC`,
	).Scan(&userAgents).Error; err != nil {
		return nil, err
	}
	return userAgents, nil
}

func QueryChatLogOutputStatsBackfillRows(ctx context.Context, tableName string, limit int) ([]ChatLogOutputStatsBackfillRow, error) {
	if limit <= 0 || !IsChatLogMonthlyTableName(tableName) {
		return []ChatLogOutputStatsBackfillRow{}, nil
	}

	rows := make([]ChatLogOutputStatsBackfillRow, 0, limit)
	if err := DB.WithContext(ctx).Raw(
		`SELECT logs.id, logs.uuid, logs.name, logs.style, logs.prompt_tokens, logs.completion_tokens, logs.total_tokens,
		        io.input, io.output_string, io.output_string_array
		   FROM `+QuoteTableName(tableName)+` AS logs
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
		  LIMIT ?`,
		"success",
		limit,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogCleanupRefsByCount(ctx context.Context, keepLatest int) ([]ChatLogCleanupRefRow, error) {
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{}, "id, uuid, created_at")
	if err != nil || union.SQL == "" {
		return []ChatLogCleanupRefRow{}, err
	}

	limitOffsetSQL, limitOffsetArgs := CountOffsetSQL(keepLatest)
	rows := make([]ChatLogCleanupRefRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT id, uuid, created_at, '' AS table_name
		   FROM (`+union.SQL+`) AS logs
		  ORDER BY created_at DESC, id DESC
		  `+limitOffsetSQL,
		limitOffsetArgs...,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogCleanupRefsBefore(ctx context.Context, cutoff time.Time) ([]ChatLogCleanupRefRow, error) {
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{EndAt: &cutoff}, "id, uuid, created_at")
	if err != nil || union.SQL == "" {
		return []ChatLogCleanupRefRow{}, err
	}

	rows := make([]ChatLogCleanupRefRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT id, uuid, created_at, '' AS table_name
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at < ?
		  ORDER BY created_at DESC, id DESC`,
		cutoff,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogHourAmount(ctx context.Context, scope ChatLogQueryScope, startAt time.Time, endAt time.Time) ([]ChatLogHourAmountRow, error) {
	union, err := BuildChatLogUnionQuery(scope, "id, created_at, total_cost")
	if err != nil || union.SQL == "" {
		return []ChatLogHourAmountRow{}, err
	}

	hourExpr := HourBucketExpr("created_at")
	rows := make([]ChatLogHourAmountRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT `+hourExpr+` AS hour_bucket,
		        COUNT(*) AS requests,
		        COALESCE(SUM(total_cost),0) AS amount
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ? AND created_at < ?
		  GROUP BY hour_bucket
		  ORDER BY hour_bucket`,
		startAt,
		endAt,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogModelUsage(ctx context.Context, startAt time.Time, endAt time.Time) ([]ChatLogModelUsageRow, error) {
	scope := ChatLogQueryScope{StartAt: &startAt, EndAt: &endAt}
	union, err := BuildChatLogUnionQuery(scope, "created_at, name, total_tokens, total_cost")
	if err != nil || union.SQL == "" {
		return []ChatLogModelUsageRow{}, err
	}

	rows := make([]ChatLogModelUsageRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT COALESCE(NULLIF(TRIM(name), ''), 'unknown') AS model,
		        COALESCE(SUM(total_tokens), 0) AS total_tokens,
		        COALESCE(SUM(total_cost), 0) AS total_cost
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ? AND created_at < ?
		  GROUP BY model
		 HAVING COALESCE(SUM(total_tokens), 0) > 0
		     OR COALESCE(SUM(total_cost), 0) > 0
		  ORDER BY total_cost DESC, total_tokens DESC, model ASC`,
		startAt,
		endAt,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func QueryChatLogDailyModelCost(ctx context.Context, startAt time.Time, endAt time.Time) ([]ChatLogDailyModelCostRow, error) {
	union, err := BuildChatLogUnionQuery(ChatLogQueryScope{StartAt: &startAt, EndAt: &endAt}, "created_at, name, total_cost")
	if err != nil || union.SQL == "" {
		return []ChatLogDailyModelCostRow{}, err
	}

	dateExpr := DateBucketExpr("created_at")
	rows := make([]ChatLogDailyModelCostRow, 0)
	if err := DB.WithContext(ctx).Raw(
		`SELECT `+dateExpr+` AS day_bucket,
		        COALESCE(NULLIF(TRIM(name), ''), 'unknown') AS model,
		        COALESCE(SUM(total_cost), 0) AS amount
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ? AND created_at < ?
		  GROUP BY day_bucket, model
		  ORDER BY day_bucket ASC, model ASC`,
		startAt,
		endAt,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ChatLogColumnsSQL 返回 chat_logs 列清单 SQL 片段。
func ChatLogColumnsSQL() string {
	return strings.Join(chatLogColumnList, ", ")
}

// ChatLogListCountColumnsSQL 返回请求日志列表计数所需的最小列集。
func ChatLogListCountColumnsSQL() string {
	return strings.Join(chatLogListCountColumnList, ", ")
}

func normalizeWhereSQL(whereSQL string) string {
	trimmed := strings.TrimSpace(whereSQL)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToUpper(trimmed), "WHERE ") {
		return " " + trimmed
	}
	return " WHERE " + trimmed
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
