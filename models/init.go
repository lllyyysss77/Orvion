package models

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var DB *gorm.DB

const gormSlowSQLThreshold = 200 * time.Millisecond

// Init 初始化数据库连接并确保当前基础表结构存在。
//
// DATABASE_DRIVER 支持:
//   - 空值/sqlite: 使用 SQLite
//   - mysql: 使用 MySQL
//
// DATABASE_DSN 支持:
//   - 空值: 默认使用 ./data/llmio.db (sqlite)
//   - sqlite://data/llmio.db / data/llmio.db / file:data/llmio.db?cache=shared
//   - mysql://user:password@127.0.0.1:3306/orvion?charset=utf8mb4&parseTime=true&loc=Local
//   - user:password@tcp(127.0.0.1:3306)/orvion?charset=utf8mb4&parseTime=true&loc=Local (需 DATABASE_DRIVER=mysql)
func Init(_ context.Context, dsn string) {
	dsn = strings.TrimSpace(dsn)

	dialector, err := buildDialector(dsn)
	if err != nil {
		panic(err)
	}
	gormConfig := &gorm.Config{
		Logger:                 newGormLogger(),
		SkipDefaultTransaction: true,
	}
	db, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		panic(err)
	}
	DB = db
	if err := configureConnectionPool(DB); err != nil {
		panic(err)
	}

	if err := DB.AutoMigrate(
		&Provider{},
		&Model{},
		&ModelWithProvider{},
		&ChatIO{},
		&TelegramAgentMessage{},
		&TelegramAgentSession{},
		&TelegramAgentToolCallLog{},
		&TelegramAgentScheduledTask{},
		&TelegramAgentSkill{},
		&AgentMemory{},
		&AuthKey{},
		&Config{},
		&ModelPrice{},
	); err != nil {
		panic(err)
	}
	_ = cleanupProviderStatusSnapshots()

	if _, err := EnsureChatLogMonthlyTable(time.Now()); err != nil {
		panic(err)
	}
	if err := EnsureAllChatLogMonthlyTableIndexes(); err != nil {
		panic(err)
	}
}

func cleanupProviderStatusSnapshots() error {
	if DB == nil {
		return nil
	}
	return DB.Where(ColumnLike("key"), "provider_status_snapshot:%").Delete(&Config{}).Error
}

func newGormLogger() gormlogger.Interface {
	baseLogger := gormlogger.New(
		log.New(io.Discard, "", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             gormSlowSQLThreshold,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: false,
			Colorful:                  false,
		},
	)
	return newSlowSQLStatsLogger(baseLogger, gormSlowSQLThreshold)
}

const sqliteScheme = "sqlite://"
const mysqlScheme = "mysql://"

type DatabaseDriver string

const (
	DatabaseDriverSQLite DatabaseDriver = "sqlite"
	DatabaseDriverMySQL  DatabaseDriver = "mysql"
)

func defaultSQLiteDSN() string {
	dataDir := filepath.Join(".", "data")
	_ = os.MkdirAll(dataDir, 0o755)
	return filepath.Join(dataDir, "llmio.db")
}

func buildDialector(dsn string) (gorm.Dialector, error) {
	return buildDialectorWithDriver(os.Getenv("DATABASE_DRIVER"), dsn)
}

func buildDialectorWithDriver(driver string, dsn string) (gorm.Dialector, error) {
	driver = normalizeDatabaseDriver(driver, dsn)
	switch driver {
	case string(DatabaseDriverMySQL):
		mysqlDSN, err := normalizeMySQLDSN(dsn)
		if err != nil {
			return nil, err
		}
		return mysql.Open(mysqlDSN), nil
	case string(DatabaseDriverSQLite):
		if strings.TrimSpace(dsn) == "" {
			dsn = defaultSQLiteDSN()
		}
		return buildSQLiteDialector(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}
}

func normalizeDatabaseDriver(driver string, dsn string) string {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver != "" {
		return driver
	}
	trimmedDSN := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(trimmedDSN, mysqlScheme) {
		return string(DatabaseDriverMySQL)
	}
	return string(DatabaseDriverSQLite)
}

func buildSQLiteDialector(dsn string) (gorm.Dialector, error) {
	sqliteDSN := normalizeSQLiteDSN(dsn)
	if sqliteDSN == "" {
		return nil, errors.New("empty sqlite dsn")
	}
	if err := ensureSQLiteDir(sqliteDSN); err != nil {
		return nil, err
	}
	return sqlite.Open(configureSQLiteDSN(sqliteDSN)), nil
}

func normalizeMySQLDSN(dsn string) (string, error) {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" {
		return "", errors.New("empty mysql dsn")
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), mysqlScheme) {
		return configureMySQLDSN(trimmed), nil
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid mysql dsn: %w", err)
	}
	if parsed.Host == "" {
		return "", errors.New("mysql dsn missing host")
	}
	databaseName := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if databaseName == "" {
		return "", errors.New("mysql dsn missing database name")
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

func normalizeSQLiteDSN(dsn string) string {
	trimmed := strings.TrimSpace(dsn)
	switch {
	case strings.HasPrefix(trimmed, sqliteScheme):
		return strings.TrimPrefix(trimmed, sqliteScheme)
	case strings.HasPrefix(trimmed, "sqlite:"):
		return strings.TrimPrefix(trimmed, "sqlite:")
	default:
		return trimmed
	}
}

const sqliteBusyTimeoutMillis = 30000

func configureSQLiteDSN(dsn string) string {
	pragmas := make([]string, 0, 4)
	query := sqliteQueryString(dsn)
	if !hasSQLitePragma(query, "busy_timeout") {
		pragmas = append(pragmas, fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMillis))
	}
	if !hasSQLitePragma(query, "foreign_keys") {
		pragmas = append(pragmas, "foreign_keys(1)")
	}
	if !isSQLiteMemoryDSN(dsn) {
		if !hasSQLitePragma(query, "journal_mode") {
			pragmas = append(pragmas, "journal_mode(WAL)")
		}
		if !hasSQLitePragma(query, "synchronous") {
			pragmas = append(pragmas, "synchronous(NORMAL)")
		}
	}
	return appendSQLitePragmas(dsn, pragmas...)
}

func configureConnectionPool(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	switch db.Dialector.Name() {
	case string(DatabaseDriverMySQL):
		sqlDB.SetMaxOpenConns(readPositiveEnvInt("DATABASE_MAX_OPEN_CONNS", 50))
		sqlDB.SetMaxIdleConns(readPositiveEnvInt("DATABASE_MAX_IDLE_CONNS", 10))
		sqlDB.SetConnMaxLifetime(time.Duration(readPositiveEnvInt("DATABASE_CONN_MAX_LIFETIME_MINUTES", 30)) * time.Minute)
	default:
		// SQLite 单文件写锁粒度较粗，单连接队列能避免同进程内连接互相抢锁。
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(0)
	}
	return nil
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

func sqliteQueryString(dsn string) string {
	idx := strings.Index(dsn, "?")
	if idx < 0 || idx == len(dsn)-1 {
		return ""
	}
	return dsn[idx+1:]
}

func hasSQLitePragma(query string, name string) bool {
	if query == "" {
		return false
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return false
	}
	target := strings.ToLower(strings.TrimSpace(name))
	for _, pragma := range values["_pragma"] {
		normalized := strings.ToLower(strings.TrimSpace(pragma))
		if normalized == target ||
			strings.HasPrefix(normalized, target+"(") ||
			strings.HasPrefix(normalized, target+"=") {
			return true
		}
	}
	return false
}

func appendSQLitePragmas(dsn string, pragmas ...string) string {
	if len(pragmas) == 0 {
		return dsn
	}
	values := url.Values{}
	for _, pragma := range pragmas {
		if strings.TrimSpace(pragma) != "" {
			values.Add("_pragma", pragma)
		}
	}
	encoded := values.Encode()
	if encoded == "" {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		if strings.HasSuffix(dsn, "?") || strings.HasSuffix(dsn, "&") {
			return dsn + encoded
		}
		return dsn + "&" + encoded
	}
	return dsn + "?" + encoded
}

func isSQLiteMemoryDSN(dsn string) bool {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == ":memory:" || strings.HasPrefix(trimmed, "file::memory:") {
		return true
	}
	values, err := url.ParseQuery(sqliteQueryString(trimmed))
	if err != nil {
		return false
	}
	return strings.EqualFold(values.Get("mode"), "memory")
}

func ensureSQLiteDir(dsn string) error {
	rawPath := dsn
	if strings.HasPrefix(rawPath, "file:") {
		rawPath = strings.TrimPrefix(rawPath, "file:")
		if idx := strings.Index(rawPath, "?"); idx >= 0 {
			rawPath = rawPath[:idx]
		}
	}
	if rawPath == "" || rawPath == ":memory:" || strings.HasPrefix(rawPath, "file::memory:") {
		return nil
	}
	dir := filepath.Dir(rawPath)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sqlite 数据目录不可写: %s: %w", dir, err)
	}
	return nil
}
