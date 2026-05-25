package admin

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
)

const defaultDatabaseTablePageSize = 30

var safeDatabaseTableNameRegexp = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type DatabaseTableInfo struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type DatabaseTableListResponse struct {
	Tables []DatabaseTableInfo `json:"tables"`
}

type DatabaseTableRowsResponse struct {
	TableName string           `json:"table_name"`
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	Total     int64            `json:"total"`
	Page      int              `json:"page"`
	PageSize  int              `json:"page_size"`
	Pages     int64            `json:"pages"`
}

func GetDatabaseTables(c *gin.Context) {
	tables, err := listVisibleDatabaseTables(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "读取数据库表失败: "+err.Error())
		return
	}

	items := make([]DatabaseTableInfo, 0, len(tables))
	for _, tableName := range tables {
		items = append(items, DatabaseTableInfo{
			Name: tableName,
			Kind: databaseTableKind(tableName),
		})
	}

	common.Success(c, DatabaseTableListResponse{Tables: items})
}

func GetDatabaseTableRows(c *gin.Context) {
	params, err := common.ParsePaginationWithDefaults(c, 1, defaultDatabaseTablePageSize)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	tableName := strings.TrimSpace(c.Param("name"))
	if !safeDatabaseTableNameRegexp.MatchString(tableName) {
		common.BadRequest(c, "非法表名")
		return
	}
	if ok, err := isVisibleDatabaseTable(c.Request.Context(), tableName); err != nil {
		common.InternalServerError(c, "校验数据库表失败: "+err.Error())
		return
	} else if !ok {
		common.NotFound(c, "数据库表不存在")
		return
	}

	total, err := countDatabaseTableRows(c.Request.Context(), tableName)
	if err != nil {
		common.InternalServerError(c, "读取表记录数失败: "+err.Error())
		return
	}
	columns, rows, err := queryDatabaseTableRows(c.Request.Context(), tableName, params)
	if err != nil {
		common.InternalServerError(c, "读取表内容失败: "+err.Error())
		return
	}

	pages := (total + int64(params.PageSize) - 1) / int64(params.PageSize)
	common.Success(c, DatabaseTableRowsResponse{
		TableName: tableName,
		Columns:   columns,
		Rows:      rows,
		Total:     total,
		Page:      params.Page,
		PageSize:  params.PageSize,
		Pages:     pages,
	})
}

func listVisibleDatabaseTables(ctx context.Context) ([]string, error) {
	if models.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	tables, err := models.DB.WithContext(ctx).Migrator().GetTables()
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(tables))
	for _, tableName := range tables {
		name := strings.TrimSpace(tableName)
		if !isVisibleDatabaseTableName(name) {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func isVisibleDatabaseTable(ctx context.Context, tableName string) (bool, error) {
	tables, err := listVisibleDatabaseTables(ctx)
	if err != nil {
		return false, err
	}
	for _, current := range tables {
		if current == tableName {
			return true, nil
		}
	}
	return false, nil
}

func isVisibleDatabaseTableName(tableName string) bool {
	name := strings.TrimSpace(tableName)
	if name == "" || !safeDatabaseTableNameRegexp.MatchString(name) {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(name), "sqlite_")
}

func databaseTableKind(tableName string) string {
	if models.IsChatLogMonthlyTableName(tableName) {
		return "日志月表"
	}
	return "业务表"
}

func countDatabaseTableRows(ctx context.Context, tableName string) (int64, error) {
	var total int64
	sqlText := "SELECT COUNT(1) FROM " + quoteDatabaseTableName(tableName)
	if err := models.DB.WithContext(ctx).Raw(sqlText).Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func queryDatabaseTableRows(ctx context.Context, tableName string, params common.PaginationParams) ([]string, []map[string]any, error) {
	offset := (params.Page - 1) * params.PageSize
	sqlText := "SELECT * FROM " + quoteDatabaseTableName(tableName) + " LIMIT ? OFFSET ?"
	sqlRows, err := models.DB.WithContext(ctx).Raw(sqlText, params.PageSize, offset).Rows()
	if err != nil {
		return nil, nil, err
	}
	defer sqlRows.Close()

	columns, err := sqlRows.Columns()
	if err != nil {
		return nil, nil, err
	}

	rows := make([]map[string]any, 0, params.PageSize)
	for sqlRows.Next() {
		item, err := scanDatabaseTableRow(sqlRows, columns)
		if err != nil {
			return nil, nil, err
		}
		rows = append(rows, item)
	}
	if err := sqlRows.Err(); err != nil {
		return nil, nil, err
	}
	return columns, rows, nil
}

func scanDatabaseTableRow(rows *sql.Rows, columns []string) (map[string]any, error) {
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}

	item := make(map[string]any, len(columns))
	for index, column := range columns {
		item[column] = normalizeDatabaseCellValue(values[index])
	}
	return item, nil
}

func normalizeDatabaseCellValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(typed) {
			return string(typed)
		}
		return base64.StdEncoding.EncodeToString(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return typed
	}
}

func quoteDatabaseTableName(tableName string) string {
	switch models.DB.Dialector.Name() {
	case "mysql":
		return "`" + strings.ReplaceAll(tableName, "`", "``") + "`"
	default:
		return `"` + strings.ReplaceAll(tableName, `"`, `""`) + `"`
	}
}
