package admin

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/service"
	"github.com/racio/orvion/service/ifacebridge"
	"gorm.io/gorm"
)

var modelConnectivityPrompts = []string{
	"今天适合喝什么茶",
	"周末有什么好安排",
	"帮我想个午餐建议",
	"最近有什么新鲜事",
}

type ModelConnectivityResult struct {
	OK          bool                                  `json:"ok"`
	Model       string                                `json:"model"`
	Prompt      string                                `json:"prompt"`
	LatencyMS   int64                                 `json:"latency_ms"`
	Total       int                                   `json:"total"`
	Available   int                                   `json:"available"`
	Unavailable int                                   `json:"unavailable"`
	Results     []ModelConnectivityProviderTestResult `json:"results"`
	Error       string                                `json:"error,omitempty"`
}

type ModelConnectivityProviderTestResult struct {
	OK            bool   `json:"ok"`
	Provider      string `json:"provider"`
	ProviderModel string `json:"provider_model"`
	LatencyMS     int64  `json:"latency_ms"`
	Error         string `json:"error,omitempty"`
}

func TestModelConnectivity(c *gin.Context) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		common.BadRequest(c, "Invalid model id")
		return
	}

	ctx := c.Request.Context()
	model, err := gorm.G[models.Model](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			common.NotFound(c, "Model not found")
			return
		}
		common.InternalServerError(c, "Failed to load model: "+err.Error())
		return
	}

	prompt := pickModelConnectivityPrompt()
	result := ModelConnectivityResult{
		Model:  model.Name,
		Prompt: prompt,
	}
	if model.Status == 0 {
		result.Error = "模型已关闭"
		common.Success(c, result)
		return
	}
	if !modelConnectivitySupportsChat(model.Capabilities) {
		result.Error = "模型未声明对话能力，连通性测试已跳过"
		common.Success(c, result)
		return
	}

	body, err := buildModelConnectivityPayload(model.Name, prompt)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	before, err := service.BeforerOpenAI(body)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if err := service.ValidateModelCapability(ctx, before.Model, "chat"); err != nil {
		result.Error = err.Error()
		common.Success(c, result)
		return
	}

	providersWithMeta, err := service.ProvidersWithMetaBymodelsName(ctx, consts.StyleOpenAI, "chat", "/v1/chat/completions", *before)
	if err != nil {
		result.Error = err.Error()
		common.Success(c, result)
		return
	}

	result = runModelConnectivityProviderTests(c, *before, providersWithMeta, models.ReqMeta{
		Header:    c.Request.Header,
		RemoteIP:  c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	}, prompt)
	common.Success(c, result)
}

func pickModelConnectivityPrompt() string {
	if len(modelConnectivityPrompts) == 0 {
		return "请回复一句测试成功"
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(modelConnectivityPrompts))))
	if err != nil {
		return modelConnectivityPrompts[int(time.Now().UnixNano()%int64(len(modelConnectivityPrompts)))]
	}
	return modelConnectivityPrompts[index.Int64()]
}

func runModelConnectivityProviderTests(c *gin.Context, before service.Before, meta *service.ProvidersWithMeta, reqMeta models.ReqMeta, prompt string) ModelConnectivityResult {
	result := ModelConnectivityResult{
		Model:  meta.ModelName,
		Prompt: prompt,
	}
	candidateIDs := sortedModelConnectivityCandidateIDs(meta)
	result.Total = len(candidateIDs)
	if result.Total == 0 {
		result.Error = "暂无启用且支持对话接口的提供商关联"
		return result
	}

	results := make([]ModelConnectivityProviderTestResult, len(candidateIDs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	start := time.Now()
	for index, id := range candidateIDs {
		index, id := index, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			singleMeta := buildSingleProviderConnectivityMeta(meta, id)
			var requestContext *gin.Context
			if c != nil {
				requestContext = c.Copy()
			}
			results[index] = testSingleModelProviderConnectivity(requestContext, before, singleMeta, reqMeta)
		}()
	}
	wg.Wait()

	result.LatencyMS = time.Since(start).Milliseconds()
	result.Results = results
	for _, item := range results {
		if item.OK {
			result.Available++
		} else {
			result.Unavailable++
		}
	}
	result.OK = result.Total > 0 && result.Unavailable == 0
	if !result.OK && result.Error == "" {
		result.Error = fmt.Sprintf("%d 个可用，%d 个不可用", result.Available, result.Unavailable)
	}
	return result
}

func sortedModelConnectivityCandidateIDs(meta *service.ProvidersWithMeta) []uint {
	ids := make([]uint, 0, len(meta.WeightItems))
	for id := range meta.WeightItems {
		if _, ok := meta.ModelWithProviderMap[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		left := meta.ModelWithProviderMap[ids[i]]
		right := meta.ModelWithProviderMap[ids[j]]
		leftProvider := meta.ProviderMap[left.ProviderID].Name
		rightProvider := meta.ProviderMap[right.ProviderID].Name
		if leftProvider == rightProvider {
			return left.ProviderModel < right.ProviderModel
		}
		return leftProvider < rightProvider
	})
	return ids
}

func buildSingleProviderConnectivityMeta(source *service.ProvidersWithMeta, modelWithProviderID uint) *service.ProvidersWithMeta {
	modelWithProvider := source.ModelWithProviderMap[modelWithProviderID]
	provider := source.ProviderMap[modelWithProvider.ProviderID]
	weight := source.WeightItems[modelWithProviderID]
	if weight <= 0 {
		weight = 1
	}
	return &service.ProvidersWithMeta{
		ModelWithProviderMap: map[uint]models.ModelWithProvider{modelWithProviderID: modelWithProvider},
		WeightItems:          map[uint]int{modelWithProviderID: weight},
		ProviderMap:          map[uint]models.Provider{provider.ID: provider},
		BridgePlans:          filterModelConnectivityBridgePlans(source, modelWithProviderID),
		ModelID:              source.ModelID,
		ModelName:            source.ModelName,
		FallbackModelID:      0,
		Endpoint:             source.Endpoint,
		MaxRetry:             1,
		TimeOut:              source.TimeOut,
		IOLog:                source.IOLog,
		Strategy:             "lottery",
		Breaker:              source.Breaker,
	}
}

func filterModelConnectivityBridgePlans(source *service.ProvidersWithMeta, modelWithProviderID uint) map[uint]ifacebridge.Plan {
	if plan, ok := source.BridgePlans[modelWithProviderID]; ok {
		return map[uint]ifacebridge.Plan{modelWithProviderID: plan}
	}
	return nil
}

func testSingleModelProviderConnectivity(c *gin.Context, before service.Before, meta *service.ProvidersWithMeta, reqMeta models.ReqMeta) ModelConnectivityProviderTestResult {
	var providerName string
	var providerModel string
	for id := range meta.WeightItems {
		modelWithProvider := meta.ModelWithProviderMap[id]
		provider := meta.ProviderMap[modelWithProvider.ProviderID]
		providerName = provider.Name
		providerModel = modelWithProvider.ProviderModel
		break
	}
	result := ModelConnectivityProviderTestResult{
		Provider:      providerName,
		ProviderModel: providerModel,
	}
	start := time.Now()
	res, log, _, _, err := service.BalanceChatWithLimiter(c, start, consts.StyleOpenAI, "/v1/chat/completions", before, meta, reqMeta)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if res == nil || res.Body == nil {
		result.Error = "上游返回空响应"
		return result
	}
	defer res.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(res.Body, 64*1024)); err != nil {
		result.Error = "读取上游响应失败: " + err.Error()
		return result
	}
	if log != nil {
		if log.ProviderName != "" {
			result.Provider = log.ProviderName
		}
		if log.ProviderModel != "" {
			result.ProviderModel = log.ProviderModel
		}
		if log.FirstChunkTimeMs > 0 {
			result.LatencyMS = int64(log.FirstChunkTimeMs)
		}
	}
	result.OK = true
	return result
}

func modelConnectivitySupportsChat(capabilities models.ModelCapabilities) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, raw := range capabilities {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "chat" {
			return true
		}
	}
	return false
}

func buildModelConnectivityPayload(modelName string, prompt string) ([]byte, error) {
	payload := map[string]any{
		"model":       modelName,
		"stream":      true,
		"temperature": 0,
		"max_tokens":  8,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("构建连通性测试请求失败: %w", err)
	}
	return body, nil
}
