package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"github.com/joho/godotenv"
	"github.com/racio/orvion/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const (
	defaultSQLitePath = "./data/llmio.db"
	defaultBatchSize  = 500
)

var chatLogTableNamePattern = regexp.MustCompile(`^chat_logs_(\d{6})$`)

type migrateOptions struct {
	sqliteDSN string
	mysqlDSN  string
	batchSize int
	clear     bool
	dryRun    bool
	verify    bool
}

type tableMigration struct {
	name  string
	model any
}

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("加载 .env 失败，将继续使用当前环境变量: %v", err)
	}

	opts := parseOptions()
	if err := run(context.Background(), opts); err != nil {
		log.Fatalf("迁移失败: %v", err)
	}
}

func parseOptions() migrateOptions {
	opts := migrateOptions{}
	flag.StringVar(&opts.sqliteDSN, "sqlite", firstNonEmpty(os.Getenv("SQLITE_DSN"), defaultSQLitePath), "SQLite 数据库路径或 DSN")
	flag.StringVar(&opts.mysqlDSN, "mysql", firstNonEmpty(os.Getenv("MYSQL_DSN"), os.Getenv("DATABASE_DSN")), "MySQL DSN")
	flag.IntVar(&opts.batchSize, "batch-size", readPositiveEnvInt("MIGRATE_BATCH_SIZE", defaultBatchSize), "每批迁移行数")
	flag.BoolVar(&opts.clear, "clear", readBoolEnv("MIGRATE_CLEAR", false), "迁移前清空 MySQL 目标表")
	flag.BoolVar(&opts.dryRun, "dry-run", readBoolEnv("MIGRATE_DRY_RUN", false), "只打印迁移计划，不写入 MySQL")
	flag.BoolVar(&opts.verify, "verify", readBoolEnv("MIGRATE_VERIFY", false), "只核对 SQLite 和 MySQL 表行数")
	flag.Parse()

	opts.sqliteDSN = strings.TrimSpace(opts.sqliteDSN)
	opts.mysqlDSN = strings.TrimSpace(opts.mysqlDSN)
	if opts.batchSize <= 0 {
		opts.batchSize = defaultBatchSize
	}
	return opts
}

func run(ctx context.Context, opts migrateOptions) error {
	if opts.sqliteDSN == "" {
		return errors.New("SQLite DSN 不能为空")
	}
	if opts.mysqlDSN == "" {
		return errors.New("MySQL DSN 不能为空，请设置 MYSQL_DSN 或 DATABASE_DSN")
	}

	sqliteDB, err := openSQLite(opts.sqliteDSN)
	if err != nil {
		return err
	}

	baseTables := baseMigrations()
	logTables, err := chatLogMigrations(sqliteDB)
	if err != nil {
		return err
	}
	allTables := append(baseTables, logTables...)

	log.Printf("迁移计划: SQLite=%s, MySQL=%s, 表数量=%d, batch=%d, clear=%v, dry_run=%v",
		maskSQLiteDSN(opts.sqliteDSN),
		maskMySQLDSN(opts.mysqlDSN),
		len(allTables),
		opts.batchSize,
		opts.clear,
		opts.dryRun,
	)
	for _, item := range allTables {
		if !sqliteDB.Migrator().HasTable(item.name) {
			log.Printf("源表 %-36s 不存在，将跳过", item.name)
			continue
		}
		count, countErr := countRows(ctx, sqliteDB, item.name)
		if countErr != nil {
			return fmt.Errorf("统计源表 %s 失败: %w", item.name, countErr)
		}
		log.Printf("源表 %-36s rows=%d", item.name, count)
	}
	if opts.dryRun {
		return nil
	}

	mysqlDB, err := openMySQL(opts.mysqlDSN)
	if err != nil {
		return err
	}
	if opts.verify {
		return verifyTables(ctx, sqliteDB, mysqlDB, allTables)
	}

	if err := mysqlDB.WithContext(ctx).AutoMigrate(migrationModels()...); err != nil {
		return fmt.Errorf("初始化 MySQL 基础表失败: %w", err)
	}
	for _, item := range logTables {
		if err := models.EnsureChatLogMonthlyTableSchemaForDB(mysqlDB.WithContext(ctx), item.name); err != nil {
			return fmt.Errorf("初始化 MySQL 日志表 %s 失败: %w", item.name, err)
		}
	}

	if opts.clear {
		if err := clearTargetTables(ctx, mysqlDB, allTables); err != nil {
			return err
		}
	}

	startedAt := time.Now()
	for _, item := range allTables {
		if err := migrateTable(ctx, sqliteDB, mysqlDB, item, opts.batchSize); err != nil {
			return err
		}
	}
	log.Printf("迁移完成，耗时 %s", time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func verifyTables(ctx context.Context, source *gorm.DB, target *gorm.DB, tables []tableMigration) error {
	log.Printf("开始核对 SQLite 与 MySQL 表行数")
	mismatches := make([]string, 0)
	for _, item := range tables {
		sourceRows := int64(0)
		if source.Migrator().HasTable(item.name) {
			count, err := countRows(ctx, source, item.name)
			if err != nil {
				return fmt.Errorf("统计 SQLite 表 %s 失败: %w", item.name, err)
			}
			sourceRows = count
		}
		targetRows := int64(0)
		if target.Migrator().HasTable(item.name) {
			count, err := countRows(ctx, target, item.name)
			if err != nil {
				return fmt.Errorf("统计 MySQL 表 %s 失败: %w", item.name, err)
			}
			targetRows = count
		}
		status := "OK"
		if sourceRows != targetRows {
			status = "MISMATCH"
			mismatches = append(mismatches, item.name)
		}
		log.Printf("核对表 %-36s sqlite=%d mysql=%d %s", item.name, sourceRows, targetRows, status)
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("迁移校验失败，行数不一致: %s", strings.Join(mismatches, ", "))
	}
	log.Printf("迁移校验通过")
	return nil
}

func openSQLite(dsn string) (*gorm.DB, error) {
	normalized := normalizeSQLiteDSN(dsn)
	db, err := gorm.Open(sqlite.Open(normalized), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 失败: %w", err)
	}
	return db, nil
}

func openMySQL(dsn string) (*gorm.DB, error) {
	normalized, err := normalizeMySQLDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(mysql.Open(normalized), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Warn),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 失败: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

func baseMigrations() []tableMigration {
	return []tableMigration{
		{name: "providers", model: &models.Provider{}},
		{name: "models", model: &models.Model{}},
		{name: "model_with_providers", model: &models.ModelWithProvider{}},
		{name: "chat_io", model: &models.ChatIO{}},
		{name: "telegram_agent_messages", model: &models.TelegramAgentMessage{}},
		{name: "telegram_agent_sessions", model: &models.TelegramAgentSession{}},
		{name: "telegram_agent_tool_call_logs", model: &models.TelegramAgentToolCallLog{}},
		{name: "telegram_agent_scheduled_tasks", model: &models.TelegramAgentScheduledTask{}},
		{name: "telegram_agent_skills", model: &models.TelegramAgentSkill{}},
		{name: "auth_keys", model: &models.AuthKey{}},
		{name: "configs", model: &models.Config{}},
		{name: "model_prices", model: &models.ModelPrice{}},
	}
}

func migrationModels() []any {
	tables := baseMigrations()
	out := make([]any, 0, len(tables))
	for _, item := range tables {
		out = append(out, item.model)
	}
	return out
}

func chatLogMigrations(db *gorm.DB) ([]tableMigration, error) {
	tables, err := listSQLiteTables(db)
	if err != nil {
		return nil, err
	}
	out := make([]tableMigration, 0)
	for _, name := range tables {
		if name == "chat_logs" || chatLogTableNamePattern.MatchString(name) {
			out = append(out, tableMigration{name: name, model: &models.ChatLog{}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func listSQLiteTables(db *gorm.DB) ([]string, error) {
	rows, err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`).Rows()
	if err != nil {
		return nil, fmt.Errorf("读取 SQLite 表列表失败: %w", err)
	}
	defer rows.Close()

	tables := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name != "" {
			tables = append(tables, name)
		}
	}
	return tables, rows.Err()
}

func clearTargetTables(ctx context.Context, db *gorm.DB, tables []tableMigration) error {
	log.Printf("开始清空 MySQL 目标表")
	db = db.WithContext(ctx)
	if err := db.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
		return err
	}
	defer db.Exec("SET FOREIGN_KEY_CHECKS = 1")

	for i := len(tables) - 1; i >= 0; i-- {
		name := tables[i].name
		if err := db.Exec("TRUNCATE TABLE " + quoteMySQLIdentifier(name)).Error; err != nil {
			return fmt.Errorf("清空表 %s 失败: %w", name, err)
		}
	}
	return nil
}

func migrateTable(ctx context.Context, source *gorm.DB, target *gorm.DB, item tableMigration, batchSize int) error {
	if !source.Migrator().HasTable(item.name) {
		log.Printf("跳过不存在源表: %s", item.name)
		return nil
	}

	total, err := countRows(ctx, source, item.name)
	if err != nil {
		return fmt.Errorf("统计表 %s 失败: %w", item.name, err)
	}
	if total == 0 {
		log.Printf("跳过空表: %s", item.name)
		return nil
	}

	log.Printf("开始迁移表: %s rows=%d", item.name, total)
	copied := int64(0)
	lastID := uint(0)
	for {
		batch := newSliceForModel(item.model, batchSize)
		err := source.WithContext(ctx).
			Table(item.name).
			Where("id > ?", lastID).
			Order("id ASC").
			Limit(batchSize).
			Find(batch).Error
		if err != nil {
			return fmt.Errorf("读取表 %s 失败: %w", item.name, err)
		}

		count := reflect.ValueOf(batch).Elem().Len()
		if count == 0 {
			break
		}

		if cleaned := sanitizeBatchUTF8(batch); cleaned > 0 {
			log.Printf("已清洗表 %s 中的非法 UTF-8 字符串字段 count=%d", item.name, cleaned)
		}

		if err := target.WithContext(ctx).
			Table(item.name).
			Clauses(clause.OnConflict{UpdateAll: true}).
			CreateInBatches(batch, batchSize).Error; err != nil {
			return fmt.Errorf("写入表 %s 失败: %w", item.name, err)
		}

		lastID = lastBatchID(batch)
		copied += int64(count)
		log.Printf("迁移进度: %-36s %d/%d", item.name, copied, total)
	}

	if err := resetMySQLAutoIncrement(ctx, target, item.name); err != nil {
		return err
	}
	log.Printf("完成迁移表: %s rows=%d", item.name, copied)
	return nil
}

func newSliceForModel(model any, capacity int) any {
	modelType := reflect.TypeOf(model)
	if modelType.Kind() != reflect.Pointer {
		panic("model must be pointer")
	}
	elemType := modelType.Elem()
	sliceType := reflect.SliceOf(elemType)
	sliceValue := reflect.MakeSlice(sliceType, 0, capacity)
	ptr := reflect.New(sliceType)
	ptr.Elem().Set(sliceValue)
	return ptr.Interface()
}

func lastBatchID(batch any) uint {
	value := reflect.ValueOf(batch).Elem()
	if value.Len() == 0 {
		return 0
	}
	last := value.Index(value.Len() - 1)
	field := last.FieldByName("ID")
	if !field.IsValid() || !field.CanUint() {
		return 0
	}
	return uint(field.Uint())
}

func sanitizeBatchUTF8(batch any) int {
	value := reflect.ValueOf(batch)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return 0
	}
	value = value.Elem()
	if value.Kind() != reflect.Slice {
		return 0
	}

	cleaned := 0
	for i := 0; i < value.Len(); i++ {
		cleaned += sanitizeValueUTF8(value.Index(i))
	}
	return cleaned
}

func sanitizeValueUTF8(value reflect.Value) int {
	if !value.IsValid() {
		return 0
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		return sanitizeValueUTF8(value.Elem())
	}

	cleaned := 0
	switch value.Kind() {
	case reflect.String:
		if value.CanSet() {
			raw := value.String()
			if !utf8.ValidString(raw) {
				value.SetString(strings.ToValidUTF8(raw, "\uFFFD"))
				cleaned++
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			cleaned += sanitizeValueUTF8(value.Field(i))
		}
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.String {
			for i := 0; i < value.Len(); i++ {
				cleaned += sanitizeValueUTF8(value.Index(i))
			}
		}
	}
	return cleaned
}

func countRows(ctx context.Context, db *gorm.DB, tableName string) (int64, error) {
	var total int64
	if err := db.WithContext(ctx).Table(tableName).Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func resetMySQLAutoIncrement(ctx context.Context, db *gorm.DB, tableName string) error {
	var maxID uint
	if err := db.WithContext(ctx).Table(tableName).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return fmt.Errorf("读取表 %s 最大 ID 失败: %w", tableName, err)
	}
	nextID := maxID + 1
	if nextID < 1 {
		nextID = 1
	}
	if err := db.WithContext(ctx).Exec(
		fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT = %d", quoteMySQLIdentifier(tableName), nextID),
	).Error; err != nil {
		return fmt.Errorf("重置表 %s 自增 ID 失败: %w", tableName, err)
	}
	return nil
}

func normalizeSQLiteDSN(dsn string) string {
	trimmed := strings.TrimSpace(dsn)
	switch {
	case trimmed == "":
		return defaultSQLitePath
	case strings.HasPrefix(strings.ToLower(trimmed), "sqlite://"):
		return strings.TrimPrefix(trimmed, "sqlite://")
	case strings.HasPrefix(strings.ToLower(trimmed), "file:"):
		return trimmed
	default:
		return filepath.Clean(trimmed)
	}
}

func normalizeMySQLDSN(dsn string) (string, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return "", errors.New("MySQL DSN 不能为空")
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "mysql://") {
		return configureMySQLDSN(trimmed), nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("MySQL DSN 无效: %w", err)
	}
	if parsed.Host == "" {
		return "", errors.New("MySQL DSN 缺少 host")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if databaseName == "" {
		return "", errors.New("MySQL DSN 缺少数据库名")
	}
	if unescaped, err := url.PathUnescape(databaseName); err == nil {
		databaseName = unescaped
	}

	username := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	auth := username
	if hasPassword {
		auth += ":" + password
	}
	if auth != "" {
		auth += "@"
	}

	query := parsed.Query()
	ensureMySQLQueryDefaults(query)
	return fmt.Sprintf("%stcp(%s)/%s?%s", auth, parsed.Host, databaseName, query.Encode()), nil
}

func configureMySQLDSN(dsn string) string {
	parts := strings.SplitN(dsn, "?", 2)
	query := url.Values{}
	if len(parts) == 2 {
		if parsed, err := url.ParseQuery(parts[1]); err == nil {
			query = parsed
		}
	}
	ensureMySQLQueryDefaults(query)
	encoded := query.Encode()
	if encoded == "" {
		return parts[0]
	}
	return parts[0] + "?" + encoded
}

func ensureMySQLQueryDefaults(query url.Values) {
	if query.Get("charset") == "" {
		query.Set("charset", "utf8mb4")
	}
	if query.Get("parseTime") == "" && query.Get("parsetime") == "" {
		query.Set("parseTime", "true")
	}
	if query.Get("loc") == "" {
		query.Set("loc", "Local")
	}
}

func quoteMySQLIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func readPositiveEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func readBoolEnv(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func maskSQLiteDSN(dsn string) string {
	return normalizeSQLiteDSN(dsn)
}

func maskMySQLDSN(dsn string) string {
	normalized, err := normalizeMySQLDSN(dsn)
	if err != nil {
		return "<invalid-mysql-dsn>"
	}
	if at := strings.LastIndex(normalized, "@"); at >= 0 {
		auth := normalized[:at]
		if colon := strings.Index(auth, ":"); colon >= 0 {
			return auth[:colon] + ":****" + normalized[at:]
		}
	}
	return normalized
}
