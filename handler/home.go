package handler

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
)

type MetricsRes struct {
	Reqs   int64 `json:"reqs"`
	Tokens int64 `json:"tokens"`
}

type MetricsSummaryRes struct {
	TotalReqs        int64   `json:"totalReqs"`
	SuccessRate      float64 `json:"successRate"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	TodayTokens      int64   `json:"todayTokens"`
	TotalAmount      float64 `json:"totalAmount"`
	TodayAmount      float64 `json:"todayAmount"`
	TodayReqs        int64   `json:"todayReqs"`
	TodaySuccessRate float64 `json:"todaySuccessRate"`
	TodaySuccessReqs int64   `json:"todaySuccessReqs"`
	TodayFailureReqs int64   `json:"todayFailureReqs"`
	TotalSuccessReqs int64   `json:"totalSuccessReqs"`
	TotalFailureReqs int64   `json:"totalFailureReqs"`
}

type RequestAmountPoint struct {
	Hour     int     `json:"hour"`
	Requests int64   `json:"requests"`
	Amount   float64 `json:"amount"`
}

type RequestAmountRes struct {
	TotalRequests int64                `json:"total_requests"`
	TotalAmount   float64              `json:"total_amount"`
	Range         string               `json:"range"`
	Points        []RequestAmountPoint `json:"points"`
}

type DailyModelCostSeries struct {
	Model   string    `json:"model"`
	Amounts []float64 `json:"amounts"`
	Total   float64   `json:"total"`
}

type DailyModelCostRes struct {
	Range  string                 `json:"range"`
	Dates  []string               `json:"dates"`
	Labels []string               `json:"labels"`
	Totals []float64              `json:"totals"`
	Series []DailyModelCostSeries `json:"series"`
}

type ModelUsageItem struct {
	Model       string  `json:"model"`
	TotalTokens int64   `json:"total_tokens"`
	TotalCost   float64 `json:"total_cost"`
}

func Metrics(c *gin.Context) {
	days, err := strconv.Atoi(c.Param("days"))
	if err != nil {
		common.BadRequest(c, "Invalid days parameter")
		return
	}

	now := time.Now()
	year, month, day := now.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -days)
	agg, err := models.QueryChatLogMetricsAgg(c.Request.Context(), models.ChatLogQueryScope{StartAt: &start}, "created_at >= ?", start)
	if err != nil {
		common.InternalServerError(c, "Failed to count requests: "+err.Error())
		return
	}
	common.Success(c, MetricsRes{
		Reqs:   agg.Reqs,
		Tokens: agg.Tokens,
	})
}

// MetricsSummary 返回系统概览用的汇总指标：
// 1) 请求总数（全量）
// 2) 请求成功率（全量）
// 3) 输入/输出 token 总数（全量）
// 4) 今日请求数（从当天 00:00 开始）
func MetricsSummary(c *gin.Context) {
	ctx := c.Request.Context()
	agg, err := models.QueryChatLogMetricsAgg(ctx, models.ChatLogQueryScope{}, "")
	if err != nil {
		common.InternalServerError(c, "Failed to sum tokens: "+err.Error())
		return
	}
	totalReqs := agg.Reqs
	totalSuccess := agg.Success
	totalFailure := totalReqs - totalSuccess

	totalAmount, err := service.GetOrInitTotalConsumedAmount(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load total amount: "+err.Error())
		return
	}

	now := time.Now()
	year, month, day := now.Date()
	startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())

	today, err := models.QueryChatLogMetricsAgg(ctx, models.ChatLogQueryScope{StartAt: &startOfDay}, "created_at >= ?", startOfDay)
	if err != nil {
		common.InternalServerError(c, "Failed to sum today metrics: "+err.Error())
		return
	}
	todayFailure := today.Reqs - today.Success

	successRate := 0.0
	if totalReqs > 0 {
		successRate = float64(totalSuccess) / float64(totalReqs) * 100
	}

	todaySuccessRate := 0.0
	if today.Reqs > 0 {
		todaySuccessRate = float64(today.Success) / float64(today.Reqs) * 100
	}

	common.Success(c, MetricsSummaryRes{
		TotalReqs:        totalReqs,
		SuccessRate:      successRate,
		PromptTokens:     agg.Prompt,
		CompletionTokens: agg.Output,
		TotalTokens:      agg.Tokens,
		TodayTokens:      today.Tokens,
		TotalAmount:      totalAmount,
		TodayAmount:      today.Amount,
		TodayReqs:        today.Reqs,
		TodaySuccessRate: todaySuccessRate,
		TodaySuccessReqs: today.Success,
		TodayFailureReqs: todayFailure,
		TotalSuccessReqs: totalSuccess,
		TotalFailureReqs: totalFailure,
	})
}

// RequestAmountTrend 返回今日请求次数与金额的小时分布
func RequestAmountTrend(c *gin.Context) {
	now := time.Now()
	year, month, day := now.Date()
	startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)
	scope := models.ChatLogQueryScope{StartAt: &startOfDay, EndAt: &endOfDay}
	rows, err := models.QueryChatLogHourAmount(c.Request.Context(), scope, startOfDay, endOfDay)
	if err != nil {
		common.InternalServerError(c, "Failed to query trend: "+err.Error())
		return
	}

	totalRequests := int64(0)
	totalAmount := 0.0
	hourMap := make(map[int]models.ChatLogHourAmountRow, len(rows))
	for _, row := range rows {
		hourMap[row.HourBucket] = row
		totalRequests += row.Requests
		totalAmount += row.Amount
	}

	points := make([]RequestAmountPoint, 0, 24)
	for hour := 0; hour < 24; hour++ {
		if row, ok := hourMap[hour]; ok {
			points = append(points, RequestAmountPoint{
				Hour:     hour,
				Requests: row.Requests,
				Amount:   row.Amount,
			})
		} else {
			points = append(points, RequestAmountPoint{
				Hour:     hour,
				Requests: 0,
				Amount:   0,
			})
		}
	}

	common.Success(c, RequestAmountRes{
		TotalRequests: totalRequests,
		TotalAmount:   totalAmount,
		Range:         "today",
		Points:        points,
	})
}

// ModelUsageSummary 返回系统概览用的按模型 token 与费用统计。
func ModelUsageSummary(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()
	endAt := now
	rangeName := strings.ToLower(strings.TrimSpace(c.DefaultQuery("range", "today")))
	startAt, ok := modelUsageRangeStart(now, rangeName)
	if !ok {
		common.BadRequest(c, "Invalid range parameter; expected today, week, or month")
		return
	}

	rows, err := models.QueryChatLogModelUsage(ctx, startAt, endAt)
	if err != nil {
		common.InternalServerError(c, "Failed to query model usage summary: "+err.Error())
		return
	}

	items := make([]ModelUsageItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, ModelUsageItem{
			Model:       row.Model,
			TotalTokens: row.TotalTokens,
			TotalCost:   row.TotalCost,
		})
	}

	common.Success(c, items)
}

func modelUsageRangeStart(now time.Time, rangeName string) (time.Time, bool) {
	year, month, day := now.Date()
	switch rangeName {
	case "today":
		return time.Date(year, month, day, 0, 0, 0, 0, now.Location()), true
	case "week":
		startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		daysSinceMonday := (int(now.Weekday()) + 6) % 7
		return startOfDay.AddDate(0, 0, -daysSinceMonday), true
	case "month":
		return time.Date(year, month, 1, 0, 0, 0, 0, now.Location()), true
	default:
		return time.Time{}, false
	}
}

// DailyModelCostTrend 返回最近 N 天的模型成本分布（按模型堆叠）
func DailyModelCostTrend(c *gin.Context) {
	const (
		defaultDays = 7
		maxDays     = 31
		defaultTop  = 5
		maxTop      = 12
		othersName  = "others"
	)

	ctx := c.Request.Context()
	days := parsePositiveInt(c.Query("days"), defaultDays, maxDays)
	top := parsePositiveInt(c.Query("top"), defaultTop, maxTop)

	now := time.Now()
	year, month, day := now.Date()
	endOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
	start := endOfDay.AddDate(0, 0, -days)

	rows, err := models.QueryChatLogDailyModelCost(ctx, start, endOfDay)
	if err != nil {
		common.InternalServerError(c, "Failed to query daily model cost: "+err.Error())
		return
	}

	dates := make([]string, 0, days)
	labels := make([]string, 0, days)
	dayIndex := make(map[string]int, days)
	for i := 0; i < days; i++ {
		dayTime := start.AddDate(0, 0, i)
		dateKey := dayTime.Format("2006-01-02")
		dates = append(dates, dateKey)
		switch {
		case i == days-1:
			labels = append(labels, "今天")
		case i == days-2:
			labels = append(labels, "昨天")
		default:
			labels = append(labels, dayTime.Format("1/2"))
		}
		dayIndex[dateKey] = i
	}

	totals := make([]float64, days)
	modelTotals := make(map[string]float64)
	dayModelCosts := make(map[string]map[string]float64)

	for _, row := range rows {
		if row.Amount <= 0 {
			continue
		}
		if _, ok := dayIndex[row.DayBucket]; !ok {
			continue
		}
		model := strings.TrimSpace(row.Model)
		if model == "" {
			model = "unknown"
		}
		byModel, ok := dayModelCosts[row.DayBucket]
		if !ok {
			byModel = make(map[string]float64)
			dayModelCosts[row.DayBucket] = byModel
		}
		byModel[model] += row.Amount
		modelTotals[model] += row.Amount
	}

	type modelTotalItem struct {
		Model string
		Total float64
	}
	modelItems := make([]modelTotalItem, 0, len(modelTotals))
	for model, total := range modelTotals {
		modelItems = append(modelItems, modelTotalItem{Model: model, Total: total})
	}
	sort.Slice(modelItems, func(i, j int) bool {
		if modelItems[i].Total == modelItems[j].Total {
			return modelItems[i].Model < modelItems[j].Model
		}
		return modelItems[i].Total > modelItems[j].Total
	})

	hasOthers := len(modelItems) > top
	selectedCount := len(modelItems)
	if selectedCount > top {
		selectedCount = top
	}

	series := make([]DailyModelCostSeries, 0, selectedCount+1)
	modelToSeries := make(map[string]int, selectedCount)
	for i := 0; i < selectedCount; i++ {
		item := modelItems[i]
		modelToSeries[item.Model] = len(series)
		series = append(series, DailyModelCostSeries{
			Model:   item.Model,
			Amounts: make([]float64, days),
		})
	}
	othersIndex := -1
	if hasOthers {
		othersIndex = len(series)
		series = append(series, DailyModelCostSeries{
			Model:   othersName,
			Amounts: make([]float64, days),
		})
	}

	for dateKey, idx := range dayIndex {
		byModel, ok := dayModelCosts[dateKey]
		if !ok {
			continue
		}
		var dayTotal float64
		var selectedTotal float64
		for model, amount := range byModel {
			dayTotal += amount
			if seriesIndex, exists := modelToSeries[model]; exists {
				series[seriesIndex].Amounts[idx] += amount
				selectedTotal += amount
			}
		}
		if hasOthers && othersIndex >= 0 {
			otherAmount := dayTotal - selectedTotal
			if otherAmount > 0 {
				series[othersIndex].Amounts[idx] += otherAmount
			}
		}
		totals[idx] = dayTotal
	}

	for i := range series {
		var total float64
		for _, amount := range series[i].Amounts {
			total += amount
		}
		series[i].Total = total
	}

	common.Success(c, DailyModelCostRes{
		Range:  "daily",
		Dates:  dates,
		Labels: labels,
		Totals: totals,
		Series: series,
	})
}

func parsePositiveInt(raw string, defaultValue, maxValue int) int {
	if strings.TrimSpace(raw) == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

type Count struct {
	Model string `json:"model"`
	Calls int64  `json:"calls"`
}

func Counts(c *gin.Context) {
	rows, err := models.QueryChatLogModelCounts(c.Request.Context(), models.ChatLogQueryScope{})
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	results := make([]Count, 0, len(rows))
	for _, row := range rows {
		results = append(results, Count{
			Model: row.Model,
			Calls: row.Calls,
		})
	}

	const topN = 5
	if len(results) > topN {
		var othersCalls int64
		for _, item := range results[topN:] {
			othersCalls += item.Calls
		}
		othersCount := Count{
			Model: "others",
			Calls: othersCalls,
		}
		results = append(results[:topN], othersCount)
	}

	common.Success(c, results)
}

type ProjectCount struct {
	Project string `json:"project"`
	Calls   int64  `json:"calls"`
}

func ProjectCounts(c *gin.Context) {
	rows, err := models.QueryChatLogAuthKeyCounts(c.Request.Context(), models.ChatLogQueryScope{})
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	ids := make([]uint, 0)
	for _, row := range rows {
		if row.AuthKeyID == 0 {
			continue
		}
		ids = append(ids, row.AuthKeyID)
	}

	keys := make([]models.AuthKey, 0)
	if len(ids) > 0 {
		if err := models.DB.
			Model(&models.AuthKey{}).
			Where("id IN ?", ids).
			Find(&keys).Error; err != nil {
			common.InternalServerError(c, err.Error())
			return
		}
	}

	keyMap := make(map[uint]string, len(keys))
	for _, key := range keys {
		keyMap[key.ID] = strings.TrimSpace(key.Name)
	}

	projectCalls := make(map[string]int64)
	for _, row := range rows {
		project := "-"
		if row.AuthKeyID == 0 {
			project = "管理员"
		} else if name, ok := keyMap[row.AuthKeyID]; ok && name != "" {
			project = name
		}
		projectCalls[project] += row.Calls
	}

	results := make([]ProjectCount, 0, len(projectCalls))
	for project, calls := range projectCalls {
		results = append(results, ProjectCount{
			Project: project,
			Calls:   calls,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Calls > results[j].Calls })

	const topN = 5
	if len(results) > topN {
		var othersCalls int64
		for _, item := range results[topN:] {
			othersCalls += item.Calls
		}
		othersCount := ProjectCount{
			Project: "others",
			Calls:   othersCalls,
		}
		results = append(results[:topN], othersCount)
	}

	common.Success(c, results)
}
