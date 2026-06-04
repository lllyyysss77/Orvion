package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg/logutil"
)

const (
	telegramAgentDefaultSystemLogLimit = 20
	telegramAgentMaxSystemLogLimit     = 80
	telegramAgentSystemLogScanLimit    = 800
	telegramAgentSystemLogWindowBytes  = 512 * 1024

	telegramAgentDefaultRequestLogLimit = 10
	telegramAgentMaxRequestLogLimit     = 50
)

var (
	telegramLogKeyValueSecretPattern = regexp.MustCompile(`(?i)((?:api[_-]?key|apikey|token|secret|password|authorization)"?\s*[:=]\s*"?)([^"\s,;}]+)`)
	telegramLogBearerSecretPattern   = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
)

func readTelegramAgentSystemLogs(_ context.Context, args telegramAgentToolCallArgs) (string, error) {
	limit := normalizeTelegramAgentSystemLogLimit(args.Limit)
	level := normalizeTelegramAgentSystemLogLevel(args.Level)
	query := strings.TrimSpace(args.Query)

	path := logutil.ResolveSystemLogFilePath()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "系统日志\n状态：日志文件不存在或尚未生成。", nil
		}
		return "", err
	}

	lines, err := readTelegramAgentLogTailLines(path, telegramAgentSystemLogScanLimit, telegramAgentSystemLogWindowBytes)
	if err != nil {
		return "", err
	}

	filtered := make([]string, 0, limit)
	for _, line := range lines {
		if !matchTelegramAgentSystemLogLine(line, level, query) {
			continue
		}
		filtered = append(filtered, sanitizeTelegramAgentLogText(line, 520))
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	var sb strings.Builder
	sb.WriteString("系统日志")
	sb.WriteString(fmt.Sprintf("\n级别：%s", telegramAgentSystemLogLevelLabel(level)))
	if query != "" {
		sb.WriteString("\n关键词：" + query)
	}
	sb.WriteString(fmt.Sprintf("\n文件大小：%s", formatTelegramAgentBytes(info.Size())))
	sb.WriteString("\n更新时间：" + formatTelegramAgentLogTime(info.ModTime()))
	if len(filtered) == 0 {
		sb.WriteString("\n\n没有找到匹配的系统日志。")
		return sb.String(), nil
	}
	sb.WriteString(fmt.Sprintf("\n结果：最近 %d 行匹配日志", len(filtered)))
	for _, line := range filtered {
		sb.WriteString("\n- " + line)
	}
	return sb.String(), nil
}

func readTelegramAgentRequestLogs(ctx context.Context, args telegramAgentToolCallArgs) (string, error) {
	if models.DB == nil {
		return "", fmt.Errorf("数据库未初始化")
	}

	limit := normalizeTelegramAgentRequestLogLimit(args.Limit)
	startAt, endAt, rangeLabel, err := buildTelegramAgentRequestLogTimeScope(args)
	if err != nil {
		return "", err
	}
	status, err := normalizeTelegramAgentRequestLogStatus(args.Status)
	if err != nil {
		return "", err
	}

	union, err := models.BuildChatLogUnionQuery(
		models.ChatLogQueryScope{StartAt: startAt, EndAt: endAt},
		models.ChatLogColumnsSQL(),
	)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(union.SQL) == "" {
		return "请求日志\n状态：当前没有请求日志。", nil
	}

	clauses := make([]string, 0, 7)
	params := make([]any, 0, 8)
	filterLabels := make([]string, 0, 6)
	if status != "" {
		clauses = append(clauses, "status = ?")
		params = append(params, status)
		filterLabels = append(filterLabels, "状态="+telegramAgentRequestLogStatusLabel(status))
	}
	if startAt != nil {
		clauses = append(clauses, "created_at >= ?")
		params = append(params, *startAt)
	}
	if endAt != nil {
		clauses = append(clauses, "created_at <= ?")
		params = append(params, *endAt)
	}
	if provider := strings.TrimSpace(args.ProviderName); provider != "" {
		clauses = append(clauses, "provider_name LIKE ?")
		params = append(params, "%"+provider+"%")
		filterLabels = append(filterLabels, "提供商="+provider)
	}
	if model := strings.TrimSpace(args.Model); model != "" {
		clauses = append(clauses, "(name LIKE ? OR provider_model LIKE ?)")
		params = append(params, "%"+model+"%", "%"+model+"%")
		filterLabels = append(filterLabels, "模型="+model)
	}
	if query := strings.TrimSpace(args.Query); query != "" {
		like := "%" + query + "%"
		clauses = append(clauses, "(name LIKE ? OR provider_model LIKE ? OR provider_name LIKE ? OR request_path LIKE ? OR error LIKE ? OR user_agent LIKE ? OR remote_ip LIKE ? OR style LIKE ?)")
		params = append(params, like, like, like, like, like, like, like, like)
		filterLabels = append(filterLabels, "关键词="+query)
	}

	sql := "SELECT " + models.ChatLogColumnsSQL() + " FROM (" + union.SQL + ") AS logs"
	if len(clauses) > 0 {
		sql += " WHERE " + strings.Join(clauses, " AND ")
	}
	sql += " ORDER BY created_at DESC, id DESC LIMIT ?"
	params = append(params, limit)

	rows := make([]models.ChatLog, 0, limit)
	if err := models.DB.WithContext(ctx).Raw(sql, params...).Scan(&rows).Error; err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("请求日志")
	if rangeLabel != "" {
		sb.WriteString("\n范围：" + rangeLabel)
	}
	if len(filterLabels) > 0 {
		sb.WriteString("\n筛选：" + strings.Join(filterLabels, "；"))
	}
	if len(rows) == 0 {
		sb.WriteString("\n\n没有找到匹配的请求日志。")
		return sb.String(), nil
	}

	sb.WriteString(fmt.Sprintf("\n结果：最近 %d 条", len(rows)))
	for _, row := range rows {
		sb.WriteString("\n- " + formatTelegramAgentRequestLogLine(row))
		if strings.TrimSpace(row.Error) != "" {
			sb.WriteString("\n  错误：" + sanitizeTelegramAgentLogText(row.Error, 260))
		}
	}
	if len(rows) == limit {
		sb.WriteString(fmt.Sprintf("\n\n已限制最多显示 %d 条，可加时间、状态、模型或提供商继续筛选。", limit))
	}
	return sb.String(), nil
}

func normalizeTelegramAgentSystemLogLimit(limit int) int {
	if limit <= 0 {
		return telegramAgentDefaultSystemLogLimit
	}
	if limit > telegramAgentMaxSystemLogLimit {
		return telegramAgentMaxSystemLogLimit
	}
	return limit
}

func normalizeTelegramAgentRequestLogLimit(limit int) int {
	if limit <= 0 {
		return telegramAgentDefaultRequestLogLimit
	}
	if limit > telegramAgentMaxRequestLogLimit {
		return telegramAgentMaxRequestLogLimit
	}
	return limit
}

func normalizeTelegramAgentSystemLogLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err":
		return "error"
	default:
		return "all"
	}
}

func normalizeTelegramAgentRequestLogStatus(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all", "全部":
		return "", nil
	case "success", "成功", "ok":
		return "success", nil
	case "error", "失败", "错误", "fail", "failed":
		return "error", nil
	default:
		return "", fmt.Errorf("请求状态只支持 success、error 或 all")
	}
}

func buildTelegramAgentRequestLogTimeScope(args telegramAgentToolCallArgs) (*time.Time, *time.Time, string, error) {
	var startAt *time.Time
	var endAt *time.Time
	labels := make([]string, 0, 2)

	if recentMinutes := args.RecentMinutes; recentMinutes > 0 {
		if recentMinutes > 60*24*30 {
			recentMinutes = 60 * 24 * 30
		}
		value := time.Now().Add(-time.Duration(recentMinutes) * time.Minute)
		startAt = &value
		labels = append(labels, fmt.Sprintf("最近 %d 分钟", recentMinutes))
	}
	if strings.TrimSpace(args.StartAt) != "" {
		value, err := parseTelegramAgentLogTime(args.StartAt, false)
		if err != nil {
			return nil, nil, "", fmt.Errorf("start_at 时间格式无效")
		}
		startAt = &value
		labels = append(labels, "开始 "+formatTelegramAgentLogTime(value))
	}
	if strings.TrimSpace(args.EndAt) != "" {
		value, err := parseTelegramAgentLogTime(args.EndAt, true)
		if err != nil {
			return nil, nil, "", fmt.Errorf("end_at 时间格式无效")
		}
		endAt = &value
		labels = append(labels, "结束 "+formatTelegramAgentLogTime(value))
	}
	if startAt != nil && endAt != nil && endAt.Before(*startAt) {
		return nil, nil, "", fmt.Errorf("结束时间不能早于开始时间")
	}
	return startAt, endAt, strings.Join(labels, "；"), nil
}

func parseTelegramAgentLogTime(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if value, err := time.Parse(layout, raw); err == nil {
			return adjustTelegramAgentDateOnlyTime(value, layout, endOfDay), nil
		}
		if value, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return adjustTelegramAgentDateOnlyTime(value, layout, endOfDay), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time")
}

func adjustTelegramAgentDateOnlyTime(value time.Time, layout string, endOfDay bool) time.Time {
	if layout == "2006-01-02" && endOfDay {
		return value.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	return value
}

func readTelegramAgentLogTailLines(path string, lineLimit int, windowBytes int64) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 {
		return nil, nil
	}

	start := info.Size() - windowBytes
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if start > 0 {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			text = text[index+1:]
		}
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil, nil
	}

	lines := strings.Split(text, "\n")
	if lineLimit > 0 && len(lines) > lineLimit {
		lines = lines[len(lines)-lineLimit:]
	}
	return lines, nil
}

func matchTelegramAgentSystemLogLine(line string, level string, query string) bool {
	normalized := strings.ToLower(line)
	if level != "" && level != "all" {
		if !strings.Contains(normalized, "level="+level) &&
			!(level == "warn" && strings.Contains(normalized, "level=warning")) {
			return false
		}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query != "" && !strings.Contains(normalized, query) {
		return false
	}
	return true
}

func formatTelegramAgentRequestLogLine(row models.ChatLog) string {
	parts := []string{
		formatTelegramAgentLogTime(row.CreatedAt),
		telegramAgentRequestLogStatusLabel(row.Status),
		telegramAgentDisplayLogValue(row.Name, "未知模型"),
		telegramAgentDisplayLogValue(row.ProviderName, "未知提供商"),
	}
	if path := strings.TrimSpace(row.RequestPath); path != "" {
		parts = append(parts, path)
	}
	if metrics := formatTelegramAgentRequestLogMetrics(row); metrics != "" {
		parts = append(parts, metrics)
	}
	return strings.Join(parts, "｜")
}

func formatTelegramAgentRequestLogMetrics(row models.ChatLog) string {
	parts := make([]string, 0, 4)
	if row.ProxyTimeMs > 0 {
		parts = append(parts, fmt.Sprintf("代理 %dms", row.ProxyTimeMs))
	}
	if row.FirstChunkTimeMs > 0 {
		parts = append(parts, fmt.Sprintf("首包 %dms", row.FirstChunkTimeMs))
	}
	if row.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens %d", row.TotalTokens))
	}
	if row.TotalCost > 0 {
		parts = append(parts, fmt.Sprintf("$%.6f", row.TotalCost))
	}
	return strings.Join(parts, "，")
}

func telegramAgentRequestLogStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success":
		return "成功"
	case "error":
		return "失败"
	default:
		if strings.TrimSpace(status) == "" {
			return "未知"
		}
		return strings.TrimSpace(status)
	}
}

func telegramAgentSystemLogLevelLabel(level string) string {
	switch level {
	case "debug":
		return "DEBUG"
	case "info":
		return "INFO"
	case "warn":
		return "WARN"
	case "error":
		return "ERROR"
	default:
		return "全部"
	}
}

func telegramAgentDisplayLogValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return sanitizeTelegramAgentLogText(value, 120)
}

func sanitizeTelegramAgentLogText(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\t", " "))
	value = telegramLogKeyValueSecretPattern.ReplaceAllString(value, "${1}已隐藏")
	value = telegramLogBearerSecretPattern.ReplaceAllString(value, "${1}已隐藏")
	return truncateTelegramToolText(value, limit)
}

func formatTelegramAgentLogTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func formatTelegramAgentBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
}
