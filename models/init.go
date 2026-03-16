package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/racio/orvion/consts"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

// Init 初始化数据库连接（默认 SQLite，支持 PostgreSQL）
// sqlite:
// - sqlite://data/llmio.db
// - data/llmio.db
// - file:data/llmio.db?cache=shared
// postgres:
// - key=value DSN: host=localhost user=postgres password=postgres dbname=llmio port=5432 sslmode=disable
// - URL: postgres://postgres:postgres@localhost:5432/llmio?sslmode=disable
func Init(ctx context.Context, dsn string) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		dsn = defaultSQLiteDSN()
	}

	dialector, err := buildDialector(dsn)
	if err != nil {
		panic(err)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		panic(err)
	}
	DB = db

	// 启动时自动创建缺失表（仅创建，不强制修改已有表结构）
	toMigrate := make([]any, 0, 8)
	modelsToCheck := []any{
		&Provider{},
		&Model{},
		&ModelWithProvider{},
		&ChatLog{},
		&ChatIO{},
		&AuthKey{},
		&Config{},
		&ModelPrice{},
	}
	for _, model := range modelsToCheck {
		if !DB.Migrator().HasTable(model) {
			toMigrate = append(toMigrate, model)
		}
	}
	if len(toMigrate) > 0 {
		if err := DB.AutoMigrate(toMigrate...); err != nil {
			panic(err)
		}
	}
	if DB.Migrator().HasTable(&Config{}) {
		if !DB.Migrator().HasIndex(&Config{}, "Key") {
			_ = DB.Migrator().CreateIndex(&Config{}, "Key")
		}
	}
	if DB.Migrator().HasTable(&AuthKey{}) {
		if !DB.Migrator().HasColumn(&AuthKey{}, "total_cost") {
			_ = DB.Migrator().AddColumn(&AuthKey{}, "TotalCost")
		}
		if !DB.Migrator().HasColumn(&AuthKey{}, "rpm_limit") {
			_ = DB.Migrator().AddColumn(&AuthKey{}, "RpmLimit")
		}
		if _, err := gorm.G[AuthKey](DB).Where("total_cost IS NULL").Update(ctx, "total_cost", 0); err != nil {
			// 忽略错误
		}
		if _, err := gorm.G[AuthKey](DB).Where("rpm_limit IS NULL").Update(ctx, "rpm_limit", 0); err != nil {
			// 忽略错误
		}
		if _, err := gorm.G[AuthKey](DB).Where("usage_count IS NULL").Update(ctx, "usage_count", 0); err != nil {
			// 忽略错误
		}
	}
	if DB.Migrator().HasTable(&ChatLog{}) {
		if !DB.Migrator().HasColumn(&ChatLog{}, "cached_tokens") {
			_ = DB.Migrator().AddColumn(&ChatLog{}, "CachedTokens")
		}
	}
	if DB.Migrator().HasTable(&Model{}) {
		if !DB.Migrator().HasColumn(&Model{}, "capabilities") {
			_ = DB.Migrator().AddColumn(&Model{}, "Capabilities")
		}
	}
	if DB.Migrator().HasTable(&Provider{}) {
		if !DB.Migrator().HasColumn(&Provider{}, "models_fetch_mode") {
			_ = DB.Migrator().AddColumn(&Provider{}, "ModelsFetchMode")
		}
		if _, err := gorm.G[Provider](DB).Where("models_fetch_mode IS NULL OR models_fetch_mode = ''").Update(ctx, "models_fetch_mode", "v1_models"); err != nil {
			// 忽略错误
		}
	}

	// 兼容性数据修复
	if _, err := gorm.G[ModelWithProvider](DB).Where("status IS NULL").Update(ctx, "status", true); err != nil {
		// 忽略错误，可能表为空
	}
	if _, err := gorm.G[ModelWithProvider](DB).Where("customer_headers IS NULL OR customer_headers = ''").Update(ctx, "customer_headers", "{}"); err != nil {
		// 忽略错误
	}
	if _, err := gorm.G[Model](DB).Where("strategy = '' OR strategy IS NULL").Update(ctx, "strategy", consts.BalancerDefault); err != nil {
		// 忽略错误
	}
	if _, err := gorm.G[Model](DB).Where("breaker IS NULL").Update(ctx, "breaker", 0); err != nil {
		// 忽略错误
	}
	if _, err := gorm.G[Model](DB).Where("status IS NULL").Update(ctx, "status", 1); err != nil {
		// 忽略错误
	}
	if _, err := gorm.G[Model](DB).Where("capabilities IS NULL OR capabilities = '' OR capabilities = '[]'").Update(ctx, "capabilities", ModelCapabilities{"chat"}); err != nil {
		// 忽略错误
	}
	if _, err := gorm.G[ChatLog](DB).Where("auth_key_id IS NULL").Update(ctx, "auth_key_id", 0); err != nil {
		// 忽略错误
	}
	if DB.Migrator().HasTable(&AuthKey{}) && DB.Migrator().HasTable(&ChatLog{}) {
		// 历史兼容：修复曾因异步记账链路丢失导致 total_cost 仍为 0 的 key
		_ = DB.WithContext(ctx).Exec(`
			UPDATE auth_keys
			SET total_cost = COALESCE((
				SELECT SUM(COALESCE(chat_logs.total_cost, 0))
				FROM chat_logs
				WHERE chat_logs.auth_key_id = auth_keys.id
			), 0)
			WHERE COALESCE(total_cost, 0) = 0
			  AND EXISTS (
				SELECT 1
				FROM chat_logs
				WHERE chat_logs.auth_key_id = auth_keys.id
				  AND COALESCE(chat_logs.total_cost, 0) > 0
			  )
		`).Error
	}
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
