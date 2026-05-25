package handler

import (
	"database/sql"
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
	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{StartAt: &start}, "id, created_at, total_tokens")
	if err != nil {
		common.InternalServerError(c, "Failed to build log query: "+err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, MetricsRes{})
		return
	}

	type metricsRow struct {
		Reqs   int64         `gorm:"column:reqs"`
		Tokens sql.NullInt64 `gorm:"column:tokens"`
	}
	var row metricsRow
	if err := models.DB.WithContext(c.Request.Context()).Raw(
		"SELECT COUNT(1) AS reqs, COALESCE(SUM(total_tokens),0) AS tokens FROM ("+union.SQL+") AS logs WHERE created_at >= ?",
		start,
	).Scan(&row).Error; err != nil {
		common.InternalServerError(c, "Failed to count requests: "+err.Error())
		return
	}
	common.Success(c, MetricsRes{
		Reqs:   row.Reqs,
		Tokens: row.Tokens.Int64,
	})
}

// MetricsSummary 返回系统概览用的汇总指标：
// 1) 请求总数（全量）
// 2) 请求成功率（全量）
// 3) 输入/输出 token 总数（全量）
// 4) 今日请求数（从当天 00:00 开始）
func MetricsSummary(c *gin.Context) {
	ctx := c.Request.Context()
	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{}, "id, created_at, status, prompt_tokens, completion_tokens, total_tokens, total_cost")
	if err != nil {
		common.InternalServerError(c, "Failed to build log query: "+err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, MetricsSummaryRes{})
		return
	}

	type tokenAgg struct {
		TotalReqs   int64         `gorm:"column:total_reqs"`
		SuccessReqs int64         `gorm:"column:success_reqs"`
		Prompt      sql.NullInt64 `gorm:"column:prompt"`
		Completion  sql.NullInt64 `gorm:"column:completion"`
		Total       sql.NullInt64 `gorm:"column:total"`
	}
	var agg tokenAgg
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT COUNT(1) AS total_reqs,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success_reqs,
		        COALESCE(SUM(prompt_tokens),0) AS prompt,
		        COALESCE(SUM(completion_tokens),0) AS completion,
		        COALESCE(SUM(total_tokens),0) AS total
		   FROM (` + union.SQL + `) AS logs
		  `,
	).Scan(&agg).Error; err != nil {
		common.InternalServerError(c, "Failed to sum tokens: "+err.Error())
		return
	}
	totalReqs := agg.TotalReqs
	totalSuccess := agg.SuccessReqs
	totalFailure := totalReqs - totalSuccess

	totalAmount, err := service.GetOrInitTotalConsumedAmount(ctx)
	if err != nil {
		common.InternalServerError(c, "Failed to load total amount: "+err.Error())
		return
	}

	now := time.Now()
	year, month, day := now.Date()
	startOfDay := time.Date(year, month, day, 0, 0, 0, 0, now.Location())

	type todayAgg struct {
		Reqs    int64           `gorm:"column:reqs"`
		Success int64           `gorm:"column:success"`
		Tokens  sql.NullInt64   `gorm:"column:tokens"`
		Amount  sql.NullFloat64 `gorm:"column:amount"`
	}
	var today todayAgg
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT COUNT(1) AS reqs,
		        COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),0) AS success,
		        COALESCE(SUM(total_tokens),0) AS tokens,
		        COALESCE(SUM(total_cost),0) AS amount
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ?`,
		startOfDay,
	).Scan(&today).Error; err != nil {
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
		PromptTokens:     agg.Prompt.Int64,
		CompletionTokens: agg.Completion.Int64,
		TotalTokens:      agg.Total.Int64,
		TodayTokens:      today.Tokens.Int64,
		TotalAmount:      totalAmount,
		TodayAmount:      today.Amount.Float64,
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
	union, err := models.BuildChatLogUnionQuery(scope, "id, created_at, total_cost")
	if err != nil {
		common.InternalServerError(c, "Failed to build log query: "+err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, RequestAmountRes{Range: "today", Points: make([]RequestAmountPoint, 0, 24)})
		return
	}

	type hourRow struct {
		HourBucket int     `gorm:"column:hour_bucket"`
		Requests   int64   `gorm:"column:requests"`
		Amount     float64 `gorm:"column:amount"`
	}
	rows := make([]hourRow, 0)
	hourExpr := "CAST(strftime('%H', created_at, 'localtime') AS INTEGER)"
	if err := models.DB.Raw(
		`SELECT `+hourExpr+` AS hour_bucket,
		        COUNT(*) AS requests,
		        COALESCE(SUM(total_cost),0) AS amount
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ? AND created_at < ?
		  GROUP BY hour_bucket
		  ORDER BY hour_bucket`,
		startOfDay,
		endOfDay,
	).Scan(&rows).Error; err != nil {
		common.InternalServerError(c, "Failed to query trend: "+err.Error())
		return
	}

	totalRequests := int64(0)
	totalAmount := 0.0
	hourMap := make(map[int]hourRow, len(rows))
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

// ModelUsageSummary 返回系统概览用的按模型累计 token 与费用统计
func ModelUsageSummary(c *gin.Context) {
	ctx := c.Request.Context()

	type modelUsageRow struct {
		Model       string  `gorm:"column:model"`
		TotalTokens int64   `gorm:"column:total_tokens"`
		TotalCost   float64 `gorm:"column:total_cost"`
	}

	rows := make([]modelUsageRow, 0)
	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{}, "name, total_tokens, total_cost")
	if err != nil {
		common.InternalServerError(c, "Failed to build log query: "+err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, []ModelUsageItem{})
		return
	}
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT COALESCE(NULLIF(TRIM(name), ''), 'unknown') AS model,
		        COALESCE(SUM(total_tokens), 0) AS total_tokens,
		        COALESCE(SUM(total_cost), 0) AS total_cost
		   FROM (` + union.SQL + `) AS logs
		  
		  GROUP BY model
		 HAVING COALESCE(SUM(total_tokens), 0) > 0
		     OR COALESCE(SUM(total_cost), 0) > 0
		  ORDER BY total_cost DESC, total_tokens DESC, model ASC`,
	).Scan(&rows).Error; err != nil {
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

	dateExpr := "strftime('%Y-%m-%d', created_at, 'localtime')"

	type modelDayRow struct {
		DayBucket string  `gorm:"column:day_bucket"`
		Model     string  `gorm:"column:model"`
		Amount    float64 `gorm:"column:amount"`
	}

	rows := make([]modelDayRow, 0)
	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{StartAt: &start, EndAt: &endOfDay}, "created_at, name, total_cost")
	if err != nil {
		common.InternalServerError(c, "Failed to build log query: "+err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, DailyModelCostRes{Range: "recent", Dates: []string{}, Labels: []string{}, Totals: []float64{}, Series: []DailyModelCostSeries{}})
		return
	}
	if err := models.DB.WithContext(ctx).Raw(
		`SELECT `+dateExpr+` AS day_bucket,
		        COALESCE(NULLIF(TRIM(name), ''), 'unknown') AS model,
		        COALESCE(SUM(total_cost), 0) AS amount
		   FROM (`+union.SQL+`) AS logs
		  WHERE created_at >= ? AND created_at < ?
		  GROUP BY day_bucket, model
		  ORDER BY day_bucket ASC, model ASC`,
		start,
		endOfDay,
	).Scan(&rows).Error; err != nil {
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
	results := make([]Count, 0)
	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{}, "name")
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, results)
		return
	}
	if err := models.DB.Raw(
		`SELECT name AS model, COUNT(*) AS calls
		   FROM (` + union.SQL + `) AS logs
		  
		  GROUP BY name
		  ORDER BY calls DESC`,
	).Scan(&results).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
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
	type authKeyCount struct {
		AuthKeyID uint  `gorm:"column:auth_key_id"`
		Calls     int64 `gorm:"column:calls"`
	}

	rows := make([]authKeyCount, 0)
	union, err := models.BuildChatLogUnionQuery(models.ChatLogQueryScope{}, "auth_key_id")
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if union.SQL == "" {
		common.Success(c, []ProjectCount{})
		return
	}
	if err := models.DB.Raw(
		`SELECT auth_key_id, COUNT(*) AS calls
		   FROM (` + union.SQL + `) AS logs
		  
		  GROUP BY auth_key_id
		  ORDER BY calls DESC`,
	).Scan(&rows).Error; err != nil {
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
