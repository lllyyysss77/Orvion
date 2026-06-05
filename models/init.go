package models

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/pkg/logutil"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var DB *gorm.DB

const gormSlowSQLThreshold = 200 * time.Millisecond

// Init 初始化数据库连接并确保当前基础表结构存在。
//
// DATABASE_DSN 支持:
//   - 空值: 默认使用 ./data/llmio.db (sqlite)
//   - sqlite://data/llmio.db / data/llmio.db / file:data/llmio.db?cache=shared
func Init(_ context.Context, dsn string) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = defaultSQLiteDSN()
	}

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
	if err := configureSQLiteConnectionPool(DB); err != nil {
		panic(err)
	}

	if err := DB.AutoMigrate(
		&Provider{},
		&Model{},
		&ModelWithProvider{},
		&ChatIO{},
		&TelegramAgentMessage{},
		&TelegramAgentSession{},
		&TelegramAgentPendingAction{},
		&TelegramAgentToolCallLog{},
		&AuthKey{},
		&Config{},
		&ModelPrice{},
	); err != nil {
		panic(err)
	}

	if _, err := EnsureChatLogMonthlyTable(time.Now()); err != nil {
		panic(err)
	}
}

func newGormLogger() gormlogger.Interface {
	writer := logutil.NewSystemLogWriter(os.Stdout)
	baseLogger := gormlogger.New(
		log.New(writer, "", log.LstdFlags),
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

func defaultSQLiteDSN() string {
	dataDir := filepath.Join(".", "data")
	_ = os.MkdirAll(dataDir, 0o755)
	return filepath.Join(dataDir, "llmio.db")
}

func buildDialector(dsn string) (gorm.Dialector, error) {
	sqliteDSN := normalizeSQLiteDSN(dsn)
	if sqliteDSN == "" {
		return nil, errors.New("empty sqlite dsn")
	}
	if err := ensureSQLiteDir(sqliteDSN); err != nil {
		return nil, err
	}
	return sqlite.Open(configureSQLiteDSN(sqliteDSN)), nil
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

func configureSQLiteConnectionPool(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	// SQLite 单文件写锁粒度较粗，单连接队列能避免同进程内连接互相抢锁。
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	return nil
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
