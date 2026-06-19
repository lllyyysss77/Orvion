package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type testDialector struct {
	name string
}

func (d testDialector) Name() string {
	return d.name
}

func (d testDialector) Initialize(*gorm.DB) error {
	return nil
}

func (d testDialector) Migrator(*gorm.DB) gorm.Migrator {
	return nil
}

func (d testDialector) DataTypeOf(*schema.Field) string {
	return ""
}

func (d testDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{}
}

func (d testDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {
	writer.WriteByte('?')
}

func (d testDialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteString(str)
}

func (d testDialector) Explain(sql string, _ ...any) string {
	return sql
}

func TestSQLDialectHelpersForSQLite(t *testing.T) {
	oldDB := DB
	t.Cleanup(func() { DB = oldDB })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	DB = db

	if got := QuoteTableName("chat_logs_202606"); got != `"chat_logs_202606"` {
		t.Fatalf("quote table = %q", got)
	}
	if got := ColumnEquals("key"); got != `"key" = ?` {
		t.Fatalf("column equals = %q", got)
	}
	if got := ColumnLike("key"); got != `"key" LIKE ?` {
		t.Fatalf("column like = %q", got)
	}
	if got := HourBucketExpr("created_at"); got != "CAST(strftime('%H', created_at, 'localtime') AS INTEGER)" {
		t.Fatalf("hour expr = %q", got)
	}
	if got := DateBucketExpr("created_at"); got != "strftime('%Y-%m-%d', created_at, 'localtime')" {
		t.Fatalf("date expr = %q", got)
	}
	sql, args := CountOffsetSQL(10)
	if sql != "LIMIT -1 OFFSET ?" || len(args) != 1 || args[0] != 10 {
		t.Fatalf("offset sql = %q args=%v", sql, args)
	}
}

func TestSQLDialectHelpersForMySQL(t *testing.T) {
	oldDB := DB
	t.Cleanup(func() { DB = oldDB })

	db, err := gorm.Open(testDialector{name: "mysql"}, &gorm.Config{})
	if err != nil {
		t.Fatalf("open mysql dry run: %v", err)
	}
	DB = db

	if got := QuoteTableName("chat_logs_202606"); got != "`chat_logs_202606`" {
		t.Fatalf("quote table = %q", got)
	}
	if got := ColumnEquals("key"); got != "`key` = ?" {
		t.Fatalf("column equals = %q", got)
	}
	if got := ColumnLike("key"); got != "`key` LIKE ?" {
		t.Fatalf("column like = %q", got)
	}
	if got := HourBucketExpr("created_at"); got != "HOUR(created_at)" {
		t.Fatalf("hour expr = %q", got)
	}
	if got := DateBucketExpr("created_at"); got != "DATE_FORMAT(created_at, '%Y-%m-%d')" {
		t.Fatalf("date expr = %q", got)
	}
	sql, args := CountOffsetSQL(10)
	if sql != "LIMIT 18446744073709551615 OFFSET ?" || len(args) != 1 || args[0] != 10 {
		t.Fatalf("offset sql = %q args=%v", sql, args)
	}
}
