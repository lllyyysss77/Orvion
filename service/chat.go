package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service/ifacebridge"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

var errMaximumRetryAttemptsReached = errors.New("maximum retry attempts reached")

type NonRetryableUpstreamStatusError struct {
	StatusCode int
	Message    string
}

func (e *NonRetryableUpstreamStatusError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func UpstreamStatusCode(err error) (int, bool) {
	var target *NonRetryableUpstreamStatusError
	if errors.As(err, &target) && target != nil && target.StatusCode > 0 {
		return target.StatusCode, true
	}
	return 0, false
}

func newNonRetryableUpstreamStatusError(statusCode int, message string) *NonRetryableUpstreamStatusError {
	return &NonRetryableUpstreamStatusError{
		StatusCode: statusCode,
		Message:    message,
	}
}

// BalanceChatWithLimiter 带限流功能的聊天负载均衡
func BalanceChatWithLimiter(c *gin.Context, start time.Time, style string, requestPath string, before Before, providersWithMeta *ProvidersWithMeta, reqMeta models.ReqMeta) (*http.Response, *models.ChatLog, Before, *ProvidersWithMeta, error) {
	return balanceChatWithFallback(c, start, style, requestPath, before, providersWithMeta, reqMeta, true, make(map[uint]struct{}))
}

func balanceChatWithFallback(c *gin.Context, start time.Time, style string, requestPath string, before Before, providersWithMeta *ProvidersWithMeta, reqMeta models.ReqMeta, enableLimiter bool, visited map[uint]struct{}) (*http.Response, *models.ChatLog, Before, *ProvidersWithMeta, error) {
	if providersWithMeta == nil {
		return nil, nil, before, providersWithMeta, errors.New("providers metadata is nil")
	}
	if providersWithMeta.ModelID > 0 {
		visited[providersWithMeta.ModelID] = struct{}{}
	}

	res, log, err := balanceChatInternal(c, start, style, requestPath, before, providersWithMeta, reqMeta, enableLimiter)
	if err == nil {
		return res, log, before, providersWithMeta, nil
	}
	if !errors.Is(err, errMaximumRetryAttemptsReached) || providersWithMeta.FallbackModelID == 0 {
		return nil, nil, before, providersWithMeta, err
	}
	if _, ok := visited[providersWithMeta.FallbackModelID]; ok {
		return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 回退模型存在循环配置", err)
	}

	ctx := context.Background()
	if c != nil {
		ctx = c.Request.Context()
	}
	fallbackModel, fallbackErr := gorm.G[models.Model](models.DB).Where("id = ?", providersWithMeta.FallbackModelID).First(ctx)
	if fallbackErr != nil {
		if errors.Is(fallbackErr, gorm.ErrRecordNotFound) {
			return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 回退模型不存在", err)
		}
		return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 查询回退模型失败: %v", err, fallbackErr)
	}
	if fallbackModel.Status == 0 {
		return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 回退模型已停用", err)
	}
	if !authContextAllowsModel(ctx, fallbackModel.Name) {
		return nil, nil, before, providersWithMeta, fmt.Errorf("%w; auth key has no permission to use fallback model %s", err, fallbackModel.Name)
	}
	if providersWithMeta.Endpoint != "" {
		if capabilityErr := ValidateModelCapability(ctx, fallbackModel.Name, providersWithMeta.Endpoint); capabilityErr != nil {
			return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 回退模型不支持当前接口: %v", err, capabilityErr)
		}
	}

	fallbackBefore, fallbackErr := before.WithModel(fallbackModel.Name)
	if fallbackErr != nil {
		return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 构建回退模型请求失败: %v", err, fallbackErr)
	}
	fallbackMeta, fallbackErr := providersWithMetaByModel(ctx, providersWithMeta.Endpoint, fallbackModel)
	if fallbackErr != nil {
		return nil, nil, before, providersWithMeta, fmt.Errorf("%w; 回退模型不可用: %v", err, fallbackErr)
	}

	slog.Info("已确认模型回退目标",
		"model", before.Model,
		"fallback_model", fallbackModel.Name,
		"endpoint", providersWithMeta.Endpoint,
	)
	slog.Warn("模型全部提供商失败，尝试回退模型",
		"model", before.Model,
		"fallback_model", fallbackModel.Name,
	)
	return balanceChatWithFallback(c, start, style, requestPath, fallbackBefore, fallbackMeta, reqMeta, false, visited)
}

func authContextAllowsModel(ctx context.Context, modelName string) bool {
	allowAll, ok := ctx.Value(consts.ContextKeyAllowAllModel).(bool)
	if !ok {
		return true
	}
	if allowAll {
		return true
	}
	allowedModels, ok := ctx.Value(consts.ContextKeyAllowModels).([]string)
	if !ok {
		return true
	}
	return slices.Contains(allowedModels, modelName)
}

// balanceChatInternal 内部聊天负载均衡实现
func balanceChatInternal(c *gin.Context, start time.Time, style string, requestPath string, before Before, providersWithMeta *ProvidersWithMeta, reqMeta models.ReqMeta, enableLimiter bool) (*http.Response, *models.ChatLog, error) {
	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	// 获取context
	var ctx context.Context
	if c != nil {
		ctx = c.Request.Context()
	} else {
		ctx = context.Background()
	}

	providerMap := providersWithMeta.ProviderMap
	if len(providersWithMeta.WeightItems) == 0 {
		return nil, nil, errMaximumRetryAttemptsReached
	}

	var proxyIP string
	if cfg, ok := runtimesvc.LoadForwardedIPOverrideConfig(ctx); ok {
		proxyIP = cfg.ProxyIP
	}

	// 收集重试过程中的err日志
	retryLog := make(chan models.ChatLog, providersWithMeta.MaxRetry)
	defer close(retryLog)

	// 使用 root context 派生,让优雅停机能终止这条异步写链;同时与请求 ctx 解耦,
	// 避免客户端提前断开就丢失已产生的重试日志。
	pkg.GoSafe("service.record_retry_log", func() { RecordRetryLog(RootContext(), retryLog) })

	// 选择负载均衡策略（含可选熔断包装）
	balancer := runtimesvc.NewBalancer(providersWithMeta.Strategy, providersWithMeta.Breaker, providersWithMeta.WeightItems)
	slog.Info("开始提供商调度",
		"model", before.Model,
		"strategy", providersWithMeta.Strategy,
		"breaker", providersWithMeta.Breaker,
		"max_retry", providersWithMeta.MaxRetry,
		"provider_candidates", len(providersWithMeta.WeightItems),
	)

	// 设置请求超时
	responseHeaderTimeout := time.Second * time.Duration(providersWithMeta.TimeOut)
	// 流式请求同样可能在上游排队或等待首个 token，不能缩短响应头等待时间。

	authKeyID, _ := ctx.Value(consts.ContextKeyAuthKeyID).(uint)
	authKeyRPMLimit, _ := ctx.Value(consts.ContextKeyAuthKeyRPMLimit).(int)
	limiterChecked := false

	timer := time.NewTimer(time.Second * time.Duration(providersWithMeta.TimeOut))
	defer timer.Stop()
	// 同一 provider 失败时先重试 N 次，再切换到其它 provider
	const perProviderMaxAttempts = 2

	attempt := 0
	for attempt < providersWithMeta.MaxRetry {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-timer.C:
			return nil, nil, errors.New("retry time out")
		default:
			// 加权负载均衡
			id, err := balancer.Pop()
			if err != nil {
				return nil, nil, err
			}

			modelWithProvider, ok := providersWithMeta.ModelWithProviderMap[id]
			if !ok {
				// 数据不一致，移除该模型避免下次重复命中
				balancer.Delete(id)
				continue
			}

			provider := providerMap[modelWithProvider.ProviderID]

			if enableLimiter && authKeyID > 0 && !limiterChecked {
				// 原子地检查配额并记账,消除 Check/Record 两步调用之间的竞态。
				// 失败请求也会占用配额,避免"刻意触发错误以绕过限流"。
				canProceed, reason, err := TryAcquireAuthKey(ctx, authKeyID, authKeyRPMLimit)
				if err != nil {
					return nil, nil, err
				}
				if !canProceed {
					slog.Info("Auth key blocked by limiter", "reason", reason)
					balancer.Reduce(id)
					continue
				}
				limiterChecked = true
			}

			slog.Info("已选择提供商",
				"model", before.Model,
				"provider", provider.Name,
				"provider_model", modelWithProvider.ProviderModel,
			)

			// 根据请求原始请求头 是否透传请求头 自定义请求头 构建新的请求头
			withHeader := modelWithProvider.WithHeader == 1
			// 解析自定义请求头
			customHeaders := make(map[string]string)
			if modelWithProvider.CustomerHeaders != "" {
				if err := json.Unmarshal([]byte(modelWithProvider.CustomerHeaders), &customHeaders); err != nil {
					slog.Error("parse custom headers error", "error", err)
				}
			}
			header := runtimesvc.BuildHeaders(reqMeta.Header, withHeader, customHeaders, before.Stream)
			if proxyIP != "" {
				header.Set("X-Forwarded-For", proxyIP)
				header.Set("X-Real-IP", proxyIP)
			}

			var lastStatus int
			var lastWas429 bool
			for providerAttempt := 0; providerAttempt < perProviderMaxAttempts && attempt < providersWithMeta.MaxRetry; providerAttempt++ {
				retry := attempt
				attempt++

				// 是否记录IO
				ioLog := 0
				if providersWithMeta.IOLog {
					ioLog = 1
				}

				log := models.ChatLog{
					Name:                before.Model,
					ProviderModel:       modelWithProvider.ProviderModel,
					ProviderName:        provider.Name,
					ModelWithProviderID: modelWithProvider.ID,
					Status:              "success",
					Style:               style,
					RequestPath:         requestPath,
					UserAgent:           reqMeta.UserAgent,
					RemoteIP:            reqMeta.RemoteIP,
					AuthKeyID:           authKeyID,
					ChatIO:              ioLog,
					Retry:               retry,
					ProxyTimeMs:         int(time.Since(start).Milliseconds()),
				}

				client, clientErr := providers.GetClientWithProxy(responseHeaderTimeout, provider.ProxyURL)
				if clientErr != nil {
					retryLog <- log.WithError(fmt.Errorf("init provider proxy client failed: %w", clientErr))
					lastStatus = 0
					lastWas429 = false
					break
				}

				effectiveStyle := style
				effectiveCtx := ctx
				upstreamRaw := before.raw
				bridgePlan, hasBridgePlan := providersWithMeta.BridgePlans[modelWithProvider.ID]
				if hasBridgePlan && bridgePlan.Enabled {
					convertedRaw, convertErr := ifacebridge.ConvertRequestBody(bridgePlan, before.raw)
					if convertErr != nil {
						retryLog <- log.WithError(fmt.Errorf("bridge request convert failed: %w", convertErr))
						lastStatus = 0
						lastWas429 = false
						break
					}
					upstreamRaw = convertedRaw
					effectiveStyle = bridgePlan.UpstreamStyle()
					effectiveCtx = ifacebridge.ApplyUpstreamContext(ctx, bridgePlan)
				}

				chatModel, err := providers.NewForStyle(effectiveStyle, provider.Config)
				if err != nil {
					retryLog <- log.WithError(err)
					lastStatus = 0
					lastWas429 = false
					break
				}

				req, err := chatModel.BuildReq(effectiveCtx, header, modelWithProvider.ProviderModel, upstreamRaw)
				if err != nil {
					retryLog <- log.WithError(err)
					// 构建请求失败属于不可恢复配置问题，直接切换
					lastStatus = 0
					lastWas429 = false
					break
				}

				res, err := client.Do(req)
				if err != nil {
					retryLog <- log.WithError(err)
					lastStatus = 0
					lastWas429 = false
					slog.Warn("提供商请求失败，准备重试",
						"model", before.Model,
						"provider", provider.Name,
						"provider_model", modelWithProvider.ProviderModel,
						"provider_attempt", providerAttempt+1,
						"global_attempt", attempt,
						"error", err,
					)
					// 网络/超时类错误：继续在同一 provider 内重试
					continue
				}

				if res.StatusCode != http.StatusOK {
					lastStatus = res.StatusCode
					lastWas429 = res.StatusCode == http.StatusTooManyRequests

					limitedBody, readErr := io.ReadAll(io.LimitReader(res.Body, runtimesvc.MaxLogBodyBytes))
					if readErr != nil {
						slog.Error("read body error", "error", readErr)
					}
					_, _ = io.Copy(io.Discard, res.Body)
					errorMessage := fmt.Sprintf("status: %d, body: %s", res.StatusCode, runtimesvc.SafeBodyTextForLog(res, limitedBody))
					retryLog <- log.WithError(errors.New(errorMessage))
					_ = res.Body.Close()
					slog.Warn("提供商返回非成功状态",
						"model", before.Model,
						"provider", provider.Name,
						"provider_model", modelWithProvider.ProviderModel,
						"status_code", res.StatusCode,
						"provider_attempt", providerAttempt+1,
						"global_attempt", attempt,
					)

					// 4xx 属于客户端请求或权限问题，直接返回给调用方，不切换提供商也不进入模型回退。
					if !runtimesvc.IsRetryableStatus(res.StatusCode) {
						return nil, nil, newNonRetryableUpstreamStatusError(res.StatusCode, errorMessage)
					}
					// 5xx：继续重试同一 provider
					continue
				}
				// 按新口径：首字耗时=从发起上游请求到收到上游 HTTP 200 响应头。
				upstreamAcceptedMs := int(time.Since(start).Milliseconds())

				// 上游可能返回 HTTP 200 但响应体为空/无效（例如仅返回 [DONE]），
				// 这类“假成功”需要转为错误并进入重试流程。
				if err := runtimesvc.ValidateSuccessfulResponseBody(res, before.Stream); err != nil {
					retryLog <- log.WithError(err)
					lastStatus = 0
					lastWas429 = false
					_ = res.Body.Close()
					continue
				}

				if hasBridgePlan && bridgePlan.Enabled {
					convertedRes, convertErr := ifacebridge.ConvertResponseBody(bridgePlan, res, before.Stream)
					if convertErr != nil {
						retryLog <- log.WithError(fmt.Errorf("bridge response convert failed: %w", convertErr))
						lastStatus = 0
						lastWas429 = false
						_ = res.Body.Close()
						continue
					}
					res = convertedRes
				}

				// success
				balancer.Success(id)
				log.FirstChunkTimeMs = upstreamAcceptedMs

				return res, &log, nil
			}

			// 同一 provider 多次失败后再切换
			if lastWas429 {
				slog.Warn("提供商因限流被降权",
					"model", before.Model,
					"provider", provider.Name,
					"provider_model", modelWithProvider.ProviderModel,
				)
				balancer.Reduce(id)
			} else {
				// 0 表示网络/构建错误；或非 429 的 HTTP 错误：移除待选
				slog.Warn("提供商已从本轮候选中移除",
					"model", before.Model,
					"provider", provider.Name,
					"provider_model", modelWithProvider.ProviderModel,
					"last_status", lastStatus,
				)
				balancer.Delete(id)
			}

			continue
		}
	}

	return nil, nil, errMaximumRetryAttemptsReached
}

func RecordRetryLog(ctx context.Context, retryLog chan models.ChatLog) {
	for log := range retryLog {
		if _, err := SaveChatLog(ctx, log); err != nil {
			slog.Error("save chat log error", "error", err)
		}
	}
}

func RecordLog(ctx context.Context, reqStart time.Time, upstreamFirstChunkMs int, reader io.ReadCloser, processer Processer, logRef models.ChatLogRef, authKeyID uint, before Before, ioLog bool, style string) {
	recordFunc := func() error {
		defer reader.Close()
		if ioLog {
			if err := gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{
				Input:   string(before.raw),
				LogId:   logRef.ID,
				LogUUID: logRef.UUID,
			}); err != nil {
				return err
			}
		}
		log, output, err := processer(ctx, reader, before.Stream, reqStart)
		if err != nil {
			return err
		}
		if upstreamFirstChunkMs > 0 {
			log.FirstChunkTimeMs = upstreamFirstChunkMs
		}
		if log.Size == 0 {
			log.Size = runtimesvc.EstimateOutputSize(output)
		}
		// 对齐 Aether fallback：仅当 usage 完全缺失时，才做估算兜底，避免覆盖已解析的真实值。
		if log.Usage.PromptTokens == 0 && log.Usage.CompletionTokens == 0 && log.Usage.TotalTokens == 0 {
			fallback := estimateUsageFromIO(style, before.Model, before.raw, output)
			log.Usage = runtimesvc.MergeUsage(log.Usage, fallback)
		}
		// 当上游只返回了输入 token，且确实有输出内容时，补齐输出 token 估算。
		// 这样不会覆盖已有真实值，只填补 completion_tokens 缺失场景。
		if log.Usage.CompletionTokens == 0 && runtimesvc.EstimateOutputSize(output) > 0 {
			fallback := estimateUsageFromIO(style, before.Model, before.raw, output)
			if fallback.CompletionTokens > 0 {
				log.Usage.CompletionTokens = fallback.CompletionTokens
			}
			if log.Usage.TotalTokens == 0 && fallback.TotalTokens > 0 {
				log.Usage.TotalTokens = fallback.TotalTokens
			}
		}
		if log.Usage.TotalTokens == 0 {
			log.Usage.TotalTokens = log.Usage.PromptTokens + log.Usage.CompletionTokens
		} else {
			computedTotal := log.Usage.PromptTokens + log.Usage.CompletionTokens
			if computedTotal > log.Usage.TotalTokens {
				log.Usage.TotalTokens = computedTotal
			}
		}
		if log.Usage.CacheHitRate == 0 {
			log.Usage.CacheHitRate = calculateCacheHitRate(log.Usage.PromptTokens, log.Usage.CachedTokens)
		}
		log.TotalCost = runtimesvc.CalculateTotalCost(ctx, before.Model, log.Usage)
		effectiveAuthKeyID := log.AuthKeyID
		if effectiveAuthKeyID == 0 {
			effectiveAuthKeyID = authKeyID
		}
		if log.AuthKeyID == 0 && authKeyID > 0 {
			log.AuthKeyID = authKeyID
		}
		if effectiveAuthKeyID > 0 && log.TotalCost > 0 {
			KeyCostUpdate(effectiveAuthKeyID, log.TotalCost, time.Now())
		}
		if err := models.UpdateMonthlyChatLogByRef(ctx, logRef, *log); err != nil {
			slog.Warn("更新日志月表失败", "error", err, "log_id", logRef.ID, "log_uuid", logRef.UUID)
		}
		if log.TotalCost > 0 {
			if err := AddTotalConsumedAmount(ctx, log.TotalCost); err != nil {
				slog.Warn("累计总金额到 config 失败", "error", err, "log_id", logRef.ID, "log_uuid", logRef.UUID)
			}
		}
		if ioLog {
			chatIO := models.ChatIO{}
			if output.OfString != "" {
				chatIO.OutputString = output.OfString
			} else if len(output.OfStringArray) > 0 {
				// 将字符串数组序列化为JSON
				if jsonBytes, err := json.Marshal(output.OfStringArray); err == nil {
					chatIO.OutputStringArray = string(jsonBytes)
				}
			}
			query := gorm.G[models.ChatIO](models.DB).Where("log_uuid = ?", logRef.UUID)
			if logRef.UUID == "" {
				query = gorm.G[models.ChatIO](models.DB).Where("log_id = ?", logRef.ID)
			}
			if _, err := query.Updates(ctx, chatIO); err != nil {
				return err
			}
		}
		return nil
	}
	if err := recordFunc(); err != nil {
		slog.Error("record log error", "error", err)
	}
}

func SaveChatLog(ctx context.Context, log models.ChatLog) (models.ChatLogRef, error) {
	// uuid 是跨月表和 chat_io 的稳定关联键，必须保证每条记录都有唯一值。
	if log.UUID == "" {
		uuid, err := pkg.GenerateRandomCharsKey(36)
		if err != nil {
			return models.ChatLogRef{}, err
		}
		log.UUID = uuid
	}

	// 极低概率下可能发生 UUID 冲突；若命中唯一约束，生成新 UUID 后重试。
	for attempt := 0; attempt < 3; attempt++ {
		ref, err := models.CreateMonthlyChatLog(ctx, log)
		if err != nil {
			// 兼容不同 driver 的错误类型：这里用 SQLSTATE 文本匹配，不引入额外依赖。
			if strings.Contains(err.Error(), "SQLSTATE 23505") {
				uuid, genErr := pkg.GenerateRandomCharsKey(36)
				if genErr != nil {
					return models.ChatLogRef{}, genErr
				}
				log.UUID = uuid
				continue
			}
			return models.ChatLogRef{}, err
		}
		if log.Status == "error" && log.ModelWithProviderID > 0 {
			ScheduleModelProviderAutoDisableCheck(log.ModelWithProviderID)
		}
		return ref, nil
	}

	return models.ChatLogRef{}, errors.New("failed to generate unique chat log uuid")
}

func BuildHeaders(source http.Header, withHeader bool, customHeaders map[string]string, stream bool) http.Header {
	return runtimesvc.BuildHeaders(source, withHeader, customHeaders, stream)
}

type ProvidersWithMeta struct {
	ModelWithProviderMap map[uint]models.ModelWithProvider
	WeightItems          map[uint]int
	ProviderMap          map[uint]models.Provider
	BridgePlans          map[uint]ifacebridge.Plan
	ModelID              uint
	ModelName            string
	FallbackModelID      uint
	Endpoint             string
	MaxRetry             int
	TimeOut              int
	IOLog                bool
	Strategy             string // 负载均衡策略
	Breaker              bool   // 是否开启熔断
}

func ProvidersWithMetaBymodelsName(ctx context.Context, logStyle string, endpoint string, requestPath string, before Before) (*ProvidersWithMeta, error) {
	model, err := gorm.G[models.Model](models.DB).Where("name = ?", before.Model).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := SaveChatLog(ctx, models.ChatLog{
				Name:        before.Model,
				Status:      "error",
				Style:       logStyle,
				RequestPath: requestPath,
				Error:       err.Error(),
			}); err != nil {
				return nil, err
			}
			return nil, errors.New("not found model " + before.Model)
		}
		return nil, err
	}
	if model.Status == 0 {
		if _, err := SaveChatLog(ctx, models.ChatLog{
			Name:        before.Model,
			Status:      "error",
			Style:       logStyle,
			RequestPath: requestPath,
			Error:       "model disabled",
		}); err != nil {
			return nil, err
		}
		return nil, errors.New("model disabled " + before.Model)
	}

	return providersWithMetaByModel(ctx, endpoint, model)
}

func providersWithMetaByModel(ctx context.Context, endpoint string, model models.Model) (*ProvidersWithMeta, error) {
	// model_with_providers.status 在数据库中是 0/1（int）
	modelWithProviderChain := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", model.ID).Where("status = ?", 1)

	modelWithProviders, err := modelWithProviderChain.Find(ctx)
	if err != nil {
		return nil, err
	}
	modelWithProviderMap := lo.KeyBy(modelWithProviders, func(mp models.ModelWithProvider) uint { return mp.ID })

	providerMap := make(map[uint]models.Provider)
	if len(modelWithProviders) > 0 {
		providerQuery := gorm.G[models.Provider](models.DB).
			Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ProviderID })).
			Where("status = ?", 1)
		providers, err := providerQuery.Find(ctx)
		if err != nil {
			return nil, err
		}
		providerMap = lo.KeyBy(providers, func(p models.Provider) uint { return p.ID })
	}

	weightItems := make(map[uint]int)
	bridgePlans := make(map[uint]ifacebridge.Plan)
	for _, mp := range modelWithProviders {
		provider, ok := providerMap[mp.ProviderID]
		if !ok {
			continue
		}
		capabilities := []string(provider.Capabilities)
		if models.ProviderSupportsEndpoint(capabilities, endpoint) {
			weightItems[mp.ID] = mp.Weight
			continue
		}
		plan, ok := ifacebridge.ResolvePlan(provider, endpoint)
		if !ok {
			continue
		}
		weightItems[mp.ID] = mp.Weight
		bridgePlans[mp.ID] = plan
	}

	if len(weightItems) == 0 {
		if model.FallbackModelID == 0 {
			return nil, errors.New("not provider for model " + model.Name)
		}
		slog.Warn("当前模型无可用提供商，准备尝试回退模型",
			"model", model.Name,
			"endpoint", endpoint,
		)
	}

	// IOLog 和 Breaker 现在是 int 类型(0/1)
	ioLog := model.IOLog == 1
	breaker := model.Breaker == 1

	return &ProvidersWithMeta{
		ModelWithProviderMap: modelWithProviderMap,
		WeightItems:          weightItems,
		ProviderMap:          providerMap,
		BridgePlans:          bridgePlans,
		ModelID:              model.ID,
		ModelName:            model.Name,
		FallbackModelID:      model.FallbackModelID,
		Endpoint:             endpoint,
		MaxRetry:             model.MaxRetry,
		TimeOut:              model.TimeOut,
		IOLog:                ioLog,
		Strategy:             model.Strategy,
		Breaker:              breaker,
	}, nil
}
