package models

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/pkg/logutil"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var DB *gorm.DB

// Init 初始化数据库连接并确保基础表结构存在（不再执行版本化迁移）。
//
// DATABASE_DSN 支持:
//   - 空值: 默认使用 ./data/llmio.db (sqlite)
//   - sqlite://data/llmio.db / data/llmio.db / file:data/llmio.db?cache=shared
//   - Postgres: host=... / postgres://... / postgresql://...
func Init(_ context.Context, dsn string) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = defaultSQLiteDSN()
	}

	dialector, err := buildDialector(dsn)
	if err != nil {
		panic(err)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: newGormLogger(),
	})
	if err != nil {
		panic(err)
	}
	DB = db

	if err := DB.AutoMigrate(
		&Provider{},
		&Model{},
		&ModelWithProvider{},
		&ChatLog{},
		&ChatIO{},
		&AuthKey{},
		&Config{},
		&ModelPrice{},
	); err != nil {
		panic(err)
	}
}

func newGormLogger() gormlogger.Interface {
	writer := logutil.NewSystemLogWriter(os.Stdout)
	return gormlogger.New(
		log.New(writer, "", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: false,
			Colorful:                  false,
		},
	)
}

const sqliteScheme = "sqlite://"

func defaultSQLiteDSN() string {
	dataDir := filepath.Join(".", "data")
	_ = os.MkdirAll(dataDir, 0o755)
	return filepath.Join(dataDir, "llmio.db")
}

func buildDialector(dsn string) (gorm.Dialector, error) {
	if isPostgresDSN(dsn) {
		return postgres.Open(dsn), nil
	}
	sqliteDSN := normalizeSQLiteDSN(dsn)
	if sqliteDSN == "" {
		return nil, errors.New("empty sqlite dsn")
	}
	if err := ensureSQLiteDir(sqliteDSN); err != nil {
		return nil, err
	}
	return sqlite.Open(sqliteDSN), nil
}

func isPostgresDSN(dsn string) bool {
	lower := strings.ToLower(strings.TrimSpace(dsn))
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		return true
	}
	return strings.Contains(lower, "host=") || strings.Contains(lower, "dbname=") || strings.Contains(lower, "user=")
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
