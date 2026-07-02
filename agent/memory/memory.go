package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	PeriodDay   = "day"
	PeriodWeek  = "week"
	PeriodMonth = "month"

	defaultContextDays    = 14
	defaultContextWeeks   = 8
	defaultContextMonths  = 6
	maxTurnTextRunes      = 6000
	maxMemoryContentRunes = 3000
)

type CompleteRequest struct {
	SystemPrompt string
	UserPrompt   string
}

type LLM interface {
	Complete(ctx context.Context, req CompleteRequest) (string, error)
}

type Turn struct {
	User       string
	Assistant  string
	OccurredAt time.Time
}

type RollupResult struct {
	WeeksCreated  int
	MonthsCreated int
	DaysDeleted   int64
	WeeksDeleted  int64
}

type periodRange struct {
	Type      string
	Key       string
	StartedAt time.Time
	EndedAt   time.Time
}

type memoryDecision struct {
	WorthRemembering bool   `json:"worth_remembering"`
	Title            string `json:"title"`
	Summary          string `json:"summary"`
}

type memorySummary struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func Enabled(cfg models.TelegramAgentConfig) bool {
	return cfg.MemoryEnabled == nil || *cfg.MemoryEnabled
}

func ProcessTurn(ctx context.Context, cfg models.TelegramAgentConfig, llm LLM, turn Turn) error {
	if !Enabled(cfg) || models.DB == nil || llm == nil {
		return nil
	}
	user := strings.TrimSpace(turn.User)
	assistant := strings.TrimSpace(turn.Assistant)
	if user == "" || assistant == "" {
		return nil
	}
	occurredAt := turn.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}

	period := dayRange(occurredAt)
	existing, _ := findMemory(ctx, PeriodDay, period.Key)
	raw, err := llm.Complete(ctx, CompleteRequest{
		SystemPrompt: dailyMemorySystemPrompt(),
		UserPrompt:   dailyMemoryUserPrompt(period.Key, existing, user, assistant),
	})
	if err != nil {
		return err
	}
	decision, err := parseMemoryDecision(raw)
	if err != nil {
		return err
	}
	if !decision.WorthRemembering {
		return nil
	}
	summary := normalizeMemoryText(decision.Summary, maxMemoryContentRunes)
	if summary == "" {
		return nil
	}
	title := normalizeMemoryTitle(decision.Title, period.Key)
	return upsertMemory(ctx, models.AgentMemory{
		PeriodType: PeriodDay,
		PeriodKey:  period.Key,
		StartedAt:  period.StartedAt,
		EndedAt:    period.EndedAt,
		Title:      title,
		Content:    summary,
	})
}

func BuildContextPrompt(ctx context.Context, cfg models.TelegramAgentConfig) (string, error) {
	if !Enabled(cfg) || models.DB == nil {
		return "", nil
	}
	months, err := listMemories(ctx, PeriodMonth, defaultContextMonths)
	if err != nil {
		return "", err
	}
	weeks, err := listMemories(ctx, PeriodWeek, defaultContextWeeks)
	if err != nil {
		return "", err
	}
	days, err := listMemories(ctx, PeriodDay, defaultContextDays)
	if err != nil {
		return "", err
	}
	if len(months)+len(weeks)+len(days) == 0 {
		return "", nil
	}

	lines := []string{
		"## 长期记忆",
		"以下是全局长期记忆摘要，包含用户偏好、项目背景、长期任务背景和稳定事实。请优先遵守，但如果用户明确给出新要求，以用户当前消息为准。",
	}
	appendMemoryGroup := func(label string, rows []models.AgentMemory) {
		if len(rows) == 0 {
			return
		}
		lines = append(lines, "", "### "+label)
		for _, row := range rows {
			content := strings.Join(strings.Fields(strings.TrimSpace(row.Content)), " ")
			if content == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s｜%s：%s", row.PeriodKey, emptyFallback(row.Title, "记忆摘要"), content))
		}
	}
	appendMemoryGroup("月记忆", months)
	appendMemoryGroup("周记忆", weeks)
	appendMemoryGroup("日记忆", days)
	return strings.Join(lines, "\n"), nil
}

func RollupCompleted(ctx context.Context, cfg models.TelegramAgentConfig, llm LLM, now time.Time) (RollupResult, error) {
	if !Enabled(cfg) || models.DB == nil || llm == nil {
		return RollupResult{}, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	result, err := rollupDaysToWeeks(ctx, cfg, llm, now)
	if err != nil {
		return result, err
	}
	monthResult, err := rollupWeeksToMonths(ctx, cfg, llm, now)
	result.MonthsCreated += monthResult.MonthsCreated
	result.WeeksDeleted += monthResult.WeeksDeleted
	if err != nil {
		return result, err
	}
	return result, nil
}

func rollupDaysToWeeks(ctx context.Context, cfg models.TelegramAgentConfig, llm LLM, now time.Time) (RollupResult, error) {
	currentWeekStart := weekStart(now)
	rows := make([]models.AgentMemory, 0)
	if err := models.DB.WithContext(ctx).
		Where("period_type = ? AND started_at < ?", PeriodDay, currentWeekStart).
		Order("started_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return RollupResult{}, err
	}
	groups := groupMemories(rows, func(row models.AgentMemory) periodRange {
		return weekRange(row.StartedAt)
	})
	var result RollupResult
	for _, group := range groups {
		if !group.period.EndedAt.After(currentWeekStart) {
			memory, err := summarizeGroup(ctx, llm, PeriodWeek, group.period, group.rows)
			if err != nil {
				return result, err
			}
			if err := upsertMemory(ctx, memory); err != nil {
				return result, err
			}
			deleted, err := deleteMemories(ctx, group.rows)
			if err != nil {
				return result, err
			}
			result.WeeksCreated++
			result.DaysDeleted += deleted
		}
	}
	return result, nil
}

func rollupWeeksToMonths(ctx context.Context, cfg models.TelegramAgentConfig, llm LLM, now time.Time) (RollupResult, error) {
	currentMonthStart := monthStart(now)
	rows := make([]models.AgentMemory, 0)
	if err := models.DB.WithContext(ctx).
		Where("period_type = ? AND started_at < ?", PeriodWeek, currentMonthStart).
		Order("started_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return RollupResult{}, err
	}
	groups := groupMemories(rows, func(row models.AgentMemory) periodRange {
		return monthRange(row.StartedAt)
	})
	var result RollupResult
	for _, group := range groups {
		if !group.period.EndedAt.After(currentMonthStart) {
			memory, err := summarizeGroup(ctx, llm, PeriodMonth, group.period, group.rows)
			if err != nil {
				return result, err
			}
			if err := upsertMemory(ctx, memory); err != nil {
				return result, err
			}
			deleted, err := deleteMemories(ctx, group.rows)
			if err != nil {
				return result, err
			}
			result.MonthsCreated++
			result.WeeksDeleted += deleted
		}
	}
	return result, nil
}

type memoryGroup struct {
	period periodRange
	rows   []models.AgentMemory
}

func groupMemories(rows []models.AgentMemory, resolve func(models.AgentMemory) periodRange) []memoryGroup {
	byKey := map[string]*memoryGroup{}
	keys := make([]string, 0)
	for _, row := range rows {
		period := resolve(row)
		key := period.Type + ":" + period.Key
		group := byKey[key]
		if group == nil {
			group = &memoryGroup{period: period}
			byKey[key] = group
			keys = append(keys, key)
		}
		group.rows = append(group.rows, row)
	}
	sort.Strings(keys)
	result := make([]memoryGroup, 0, len(keys))
	for _, key := range keys {
		result = append(result, *byKey[key])
	}
	return result
}

func summarizeGroup(ctx context.Context, llm LLM, periodType string, period periodRange, rows []models.AgentMemory) (models.AgentMemory, error) {
	existing, _ := findMemory(ctx, periodType, period.Key)
	raw, err := llm.Complete(ctx, CompleteRequest{
		SystemPrompt: rollupMemorySystemPrompt(periodType),
		UserPrompt:   rollupMemoryUserPrompt(periodType, period.Key, existing, rows),
	})
	if err != nil {
		return models.AgentMemory{}, err
	}
	summary, err := parseMemorySummary(raw)
	if err != nil {
		return models.AgentMemory{}, err
	}
	content := normalizeMemoryText(summary.Summary, maxMemoryContentRunes)
	if content == "" {
		return models.AgentMemory{}, errors.New("长期记忆汇总为空")
	}
	return models.AgentMemory{
		PeriodType: periodType,
		PeriodKey:  period.Key,
		StartedAt:  period.StartedAt,
		EndedAt:    period.EndedAt,
		Title:      normalizeMemoryTitle(summary.Title, period.Key),
		Content:    content,
	}, nil
}

func findMemory(ctx context.Context, periodType string, periodKey string) (models.AgentMemory, error) {
	var row models.AgentMemory
	if models.DB == nil {
		return row, gorm.ErrRecordNotFound
	}
	err := models.DB.WithContext(ctx).
		Where("period_type = ? AND period_key = ?", periodType, periodKey).
		First(&row).Error
	return row, err
}

func upsertMemory(ctx context.Context, row models.AgentMemory) error {
	if models.DB == nil {
		return nil
	}
	row.PeriodType = strings.TrimSpace(row.PeriodType)
	row.PeriodKey = strings.TrimSpace(row.PeriodKey)
	row.Title = normalizeMemoryTitle(row.Title, row.PeriodKey)
	row.Content = normalizeMemoryText(row.Content, maxMemoryContentRunes)
	if row.PeriodType == "" || row.PeriodKey == "" || row.Content == "" {
		return nil
	}

	var existing models.AgentMemory
	err := models.DB.WithContext(ctx).
		Where("period_type = ? AND period_key = ?", row.PeriodType, row.PeriodKey).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.DB.WithContext(ctx).Create(&row).Error
	}
	if err != nil {
		return err
	}

	updates := map[string]any{
		"started_at": row.StartedAt,
		"ended_at":   row.EndedAt,
		"title":      row.Title,
		"content":    row.Content,
		"updated_at": time.Now(),
	}
	return models.DB.WithContext(ctx).Model(&models.AgentMemory{}).Where("id = ?", existing.ID).Updates(updates).Error
}

func deleteMemories(ctx context.Context, rows []models.AgentMemory) (int64, error) {
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		if row.ID != 0 {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	result := models.DB.WithContext(ctx).Where("id IN ?", ids).Delete(&models.AgentMemory{})
	return result.RowsAffected, result.Error
}

func listMemories(ctx context.Context, periodType string, limit int) ([]models.AgentMemory, error) {
	rows := make([]models.AgentMemory, 0)
	if limit <= 0 {
		return rows, nil
	}
	err := models.DB.WithContext(ctx).
		Where("period_type = ?", periodType).
		Order("started_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func dayRange(value time.Time) periodRange {
	local := value.In(time.Local)
	year, month, day := local.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, local.Location())
	return periodRange{
		Type:      PeriodDay,
		Key:       start.Format("2006-01-02"),
		StartedAt: start,
		EndedAt:   start.AddDate(0, 0, 1),
	}
}

func weekRange(value time.Time) periodRange {
	start := weekStart(value)
	year, week := start.ISOWeek()
	return periodRange{
		Type:      PeriodWeek,
		Key:       fmt.Sprintf("%04d-W%02d", year, week),
		StartedAt: start,
		EndedAt:   start.AddDate(0, 0, 7),
	}
}

func weekStart(value time.Time) time.Time {
	local := value.In(time.Local)
	year, month, day := local.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, local.Location())
	weekday := int(start.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return start.AddDate(0, 0, -(weekday - 1))
}

func monthRange(value time.Time) periodRange {
	start := monthStart(value)
	return periodRange{
		Type:      PeriodMonth,
		Key:       start.Format("2006-01"),
		StartedAt: start,
		EndedAt:   start.AddDate(0, 1, 0),
	}
}

func monthStart(value time.Time) time.Time {
	local := value.In(time.Local)
	year, month, _ := local.Date()
	return time.Date(year, month, 1, 0, 0, 0, 0, local.Location())
}

func normalizeMemoryTitle(value string, fallback string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		value = fallback
	}
	runes := []rune(value)
	if len(runes) > 80 {
		value = string(runes[:80])
	}
	return value
}

func normalizeMemoryText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if maxRunes <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return value
}

func limitText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "\n（内容已截断）"
	}
	return value
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func parseMemoryDecision(raw string) (memoryDecision, error) {
	var decision memoryDecision
	if err := unmarshalMemoryJSON(raw, &decision); err != nil {
		return decision, err
	}
	decision.Title = strings.TrimSpace(decision.Title)
	decision.Summary = strings.TrimSpace(decision.Summary)
	return decision, nil
}

func parseMemorySummary(raw string) (memorySummary, error) {
	var summary memorySummary
	if err := unmarshalMemoryJSON(raw, &summary); err != nil {
		return summary, err
	}
	summary.Title = strings.TrimSpace(summary.Title)
	summary.Summary = strings.TrimSpace(summary.Summary)
	return summary, nil
}

func unmarshalMemoryJSON(raw string, target any) error {
	text := strings.TrimSpace(raw)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)
	if err := json.Unmarshal([]byte(text), target); err == nil {
		return nil
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(text[start:end+1]), target); err == nil {
			return nil
		}
	}
	return errors.New("模型未返回合法的长期记忆 JSON")
}
