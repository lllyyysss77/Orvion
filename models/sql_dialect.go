package models

import (
	"fmt"
	"strings"
)

func CurrentDatabaseDriver() DatabaseDriver {
	if DB == nil || DB.Dialector == nil {
		return DatabaseDriverSQLite
	}
	switch strings.ToLower(strings.TrimSpace(DB.Dialector.Name())) {
	case string(DatabaseDriverMySQL):
		return DatabaseDriverMySQL
	default:
		return DatabaseDriverSQLite
	}
}

func IsMySQL() bool {
	return CurrentDatabaseDriver() == DatabaseDriverMySQL
}

func QuoteIdentifier(name string) string {
	if IsMySQL() {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func QuoteTableName(tableName string) string {
	return QuoteIdentifier(tableName)
}

func ColumnEquals(column string) string {
	return QuoteIdentifier(column) + " = ?"
}

func ColumnLike(column string) string {
	return QuoteIdentifier(column) + " LIKE ?"
}

func HourBucketExpr(column string) string {
	column = strings.TrimSpace(column)
	if column == "" {
		column = "created_at"
	}
	if IsMySQL() {
		return fmt.Sprintf("HOUR(%s)", column)
	}
	return fmt.Sprintf("CAST(strftime('%%H', %s, 'localtime') AS INTEGER)", column)
}

func DateBucketExpr(column string) string {
	column = strings.TrimSpace(column)
	if column == "" {
		column = "created_at"
	}
	if IsMySQL() {
		return fmt.Sprintf("DATE_FORMAT(%s, '%%Y-%%m-%%d')", column)
	}
	return fmt.Sprintf("strftime('%%Y-%%m-%%d', %s, 'localtime')", column)
}

func CountOffsetSQL(limitOffset int) (string, []any) {
	if limitOffset <= 0 {
		return "", nil
	}
	if IsMySQL() {
		return "LIMIT 18446744073709551615 OFFSET ?", []any{limitOffset}
	}
	return "LIMIT -1 OFFSET ?", []any{limitOffset}
}
