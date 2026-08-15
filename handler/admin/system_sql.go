package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
)

const (
	databaseSQLTimeout   = 30 * time.Second
	databaseSQLMaxLength = 100_000
	databaseSQLMaxRows   = 1_000
)

var databaseSQLVerbRegexp = regexp.MustCompile(`(?i)^(SELECT|INSERT|UPDATE|DELETE)\b`)

type DatabaseSQLRequest struct {
	SQL string `json:"sql"`
}

type DatabaseSQLResponse struct {
	StatementType string           `json:"statement_type"`
	Columns       []string         `json:"columns"`
	Rows          []map[string]any `json:"rows"`
	RowsAffected  int64            `json:"rows_affected"`
	Truncated     bool             `json:"truncated"`
	DurationMS    int64            `json:"duration_ms"`
}

func ExecuteDatabaseSQL(c *gin.Context) {
	var request DatabaseSQLRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.BadRequest(c, "SQL 请求格式无效")
		return
	}

	sqlText, statementType, err := normalizeDatabaseSQL(request.SQL)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	if models.DB == nil {
		common.InternalServerError(c, "数据库未初始化")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), databaseSQLTimeout)
	defer cancel()
	startedAt := time.Now()
	result := DatabaseSQLResponse{
		StatementType: statementType,
		Columns:       []string{},
		Rows:          []map[string]any{},
	}

	if statementType == "select" {
		columns, rows, truncated, queryErr := executeDatabaseSelect(ctx, sqlText)
		if queryErr != nil {
			common.BadRequest(c, "SQL 执行失败: "+queryErr.Error())
			return
		}
		result.Columns = columns
		result.Rows = rows
		result.RowsAffected = int64(len(rows))
		result.Truncated = truncated
	} else {
		execResult := models.DB.WithContext(ctx).Exec(sqlText)
		if execResult.Error != nil {
			common.BadRequest(c, "SQL 执行失败: "+execResult.Error.Error())
			return
		}
		result.RowsAffected = execResult.RowsAffected
	}

	result.DurationMS = time.Since(startedAt).Milliseconds()
	common.Success(c, result)
}

func normalizeDatabaseSQL(raw string) (string, string, error) {
	sqlText := strings.TrimSpace(raw)
	if sqlText == "" {
		return "", "", errors.New("SQL 不能为空")
	}
	if len(sqlText) > databaseSQLMaxLength {
		return "", "", fmt.Errorf("SQL 长度不能超过 %d 个字符", databaseSQLMaxLength)
	}
	if !utf8.ValidString(sqlText) || strings.ContainsRune(sqlText, '\x00') {
		return "", "", errors.New("SQL 包含无效字符")
	}

	// 允许输入末尾一个分号，但禁止多语句执行和注释绕过语句类型校验。
	if strings.HasSuffix(sqlText, ";") {
		sqlText = strings.TrimSpace(strings.TrimSuffix(sqlText, ";"))
	}
	if strings.ContainsRune(sqlText, ';') {
		return "", "", errors.New("仅允许执行单条 SQL，禁止多语句")
	}
	if strings.Contains(sqlText, "--") || strings.Contains(sqlText, "/*") || strings.Contains(sqlText, "*/") {
		return "", "", errors.New("SQL 不允许包含注释")
	}

	matches := databaseSQLVerbRegexp.FindStringSubmatch(sqlText)
	if len(matches) != 2 {
		return "", "", errors.New("仅允许执行 SELECT、INSERT、UPDATE 或 DELETE")
	}
	return sqlText, strings.ToLower(matches[1]), nil
}

func executeDatabaseSelect(ctx context.Context, sqlText string) ([]string, []map[string]any, bool, error) {
	rows, err := models.DB.WithContext(ctx).Raw(sqlText).Rows()
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	result := make([]map[string]any, 0, min(databaseSQLMaxRows, 64))
	truncated := false
	for rows.Next() {
		if len(result) >= databaseSQLMaxRows {
			truncated = true
			break
		}
		item, scanErr := scanDatabaseTableRow(rows, columns)
		if scanErr != nil {
			return nil, nil, false, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return columns, result, truncated, nil
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
