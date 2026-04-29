package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/pkg"
	"github.com/racio/orvion/providers"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

// BalanceChatWithLimiter 带限流功能的聊天负载均衡
func BalanceChatWithLimiter(c *gin.Context, start time.Time, style string, before Before, providersWithMeta *ProvidersWithMeta, reqMeta models.ReqMeta) (*http.Response, *models.ChatLog, error) {
	return balanceChatInternal(c, start, style, before, providersWithMeta, reqMeta, true)
}

// balanceChatInternal 内部聊天负载均衡实现
func balanceChatInternal(c *gin.Context, start time.Time, style string, before Before, providersWithMeta *ProvidersWithMeta, reqMeta models.ReqMeta, enableLimiter bool) (*http.Response, *models.ChatLog, error) {
	slog.Info("request", "model", before.Model, "stream", before.Stream, "tool_call", before.toolCall, "structured_output", before.structuredOutput, "image", before.image)

	// 获取context
	var ctx context.Context
	if c != nil {
		ctx = c.Request.Context()
	} else {
		ctx = context.Background()
	}

	providerMap := providersWithMeta.ProviderMap

	var proxyIP string
	if cfg, ok := runtimesvc.LoadAnthropicProxyIPConfig(ctx); ok {
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

	// 设置请求超时
	responseHeaderTimeout := time.Second * time.Duration(providersWithMeta.TimeOut)
	// 流式超时时间缩短
	if before.Stream {
		responseHeaderTimeout = responseHeaderTimeout / 3
	}
	client := providers.GetClient(responseHeaderTimeout)

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
					slog.Info("Auth key blocked by limiter", "auth_key_id", authKeyID, "reason", reason)
					balancer.Reduce(id)
					continue
				}
				limiterChecked = true
			}

			slog.Info("using provider", "provider", provider.Name, "model", modelWithProvider.ProviderModel)

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
					UserAgent:           reqMeta.UserAgent,
					RemoteIP:            reqMeta.RemoteIP,
					AuthKeyID:           authKeyID,
					ChatIO:              ioLog,
					Retry:               retry,
					ProxyTimeMs:         int(time.Since(start).Milliseconds()),
				}

				chatModel, err := providers.NewForStyle(style, provider.Config)
				if err != nil {
					retryLog <- log.WithError(err)
					lastStatus = 0
					lastWas429 = false
					break
				}

				req, err := chatModel.BuildReq(ctx, header, modelWithProvider.ProviderModel, before.raw)
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
					retryLog <- log.WithError(fmt.Errorf("status: %d, body: %s", res.StatusCode, runtimesvc.SafeBodyTextForLog(res, limitedBody)))
					_ = res.Body.Close()

					// 非可重试的 4xx：直接切换（不浪费同 provider 的 3 次机会）
					if !runtimesvc.IsRetryableStatus(res.StatusCode) {
						break
					}
					// 429/5xx/408：继续重试同一 provider
					continue
				}

				// 上游可能返回 HTTP 200 但响应体为空/无效（例如仅返回 [DONE]），
				// 这类“假成功”需要转为错误并进入重试流程。
				if err := runtimesvc.ValidateSuccessfulResponseBody(res, before.Stream); err != nil {
					retryLog <- log.WithError(err)
					lastStatus = 0
					lastWas429 = false
					_ = res.Body.Close()
					continue
				}

				// success
				balancer.Success(id)

				return res, &log, nil
			}

			// 同一 provider 多次失败后再切换
			if lastWas429 {
				balancer.Reduce(id)
			} else {
				// 0 表示网络/构建错误；或非 429 的 HTTP 错误：移除待选
				_ = lastStatus
				balancer.Delete(id)
			}

			continue
		}
	}

	return nil, nil, errors.New("maximum retry attempts reached")
}

func RecordRetryLog(ctx context.Context, retryLog chan models.ChatLog) {
	for log := range retryLog {
		if _, err := SaveChatLog(ctx, log); err != nil {
			slog.Error("save chat log error", "error", err)
		}
	}
}

func RecordLog(ctx context.Context, reqStart time.Time, reader io.ReadCloser, processer Processer, logId uint, authKeyID uint, before Before, ioLog bool, style string) {
	recordFunc := func() error {
		defer reader.Close()
		if ioLog {
			if err := gorm.G[models.ChatIO](models.DB).Create(ctx, &models.ChatIO{
				Input: string(before.raw),
				LogId: logId,
			}); err != nil {
				return err
			}
		}
		log, output, err := processer(ctx, reader, before.Stream, reqStart)
		if err != nil {
			return err
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
		if _, err := gorm.G[models.ChatLog](models.DB).Where("id = ?", logId).Updates(ctx, *log); err != nil {
			return err
		}
		if log.TotalCost > 0 {
			if err := AddTotalConsumedAmount(ctx, log.TotalCost); err != nil {
				slog.Warn("累计总金额到 config 失败", "error", err, "log_id", logId)
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
			if _, err := gorm.G[models.ChatIO](models.DB).Where("log_id = ?", logId).Updates(ctx, chatIO); err != nil {
				return err
			}
		}
		return nil
	}
	if err := recordFunc(); err != nil {
		slog.Error("record log error", "error", err)
	}
}

func SaveChatLog(ctx context.Context, log models.ChatLog) (uint, error) {
	// chat_logs.uuid 在数据库中是 NOT NULL UNIQUE，必须保证每条记录都有唯一值。
	if log.UUID == "" {
		uuid, err := pkg.GenerateRandomCharsKey(36)
		if err != nil {
			return 0, err
		}
		log.UUID = uuid
	}

	// 极低概率下可能发生 UUID 冲突；若命中唯一约束，生成新 UUID 后重试。
	for attempt := 0; attempt < 3; attempt++ {
		if err := gorm.G[models.ChatLog](models.DB).Create(ctx, &log); err != nil {
			// 兼容不同 driver 的错误类型：这里用 SQLSTATE 文本匹配，不引入额外依赖。
			if strings.Contains(err.Error(), "SQLSTATE 23505") {
				uuid, genErr := pkg.GenerateRandomCharsKey(36)
				if genErr != nil {
					return 0, genErr
				}
				log.UUID = uuid
				continue
			}
			return 0, err
		}
		if log.Status == "error" && log.ModelWithProviderID > 0 {
			modelWithProviderID := log.ModelWithProviderID
			go func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("auto disable goroutine panic", "recover", r, "model_with_provider_id", modelWithProviderID)
					}
				}()
				// 基于 root ctx 派生 5s 超时,既有 shutdown 感知又防止长挂。
				autoCtx, cancel := context.WithTimeout(RootContext(), 5*time.Second)
				defer cancel()
				if err := TriggerModelProviderAutoDisableIfNeeded(autoCtx, modelWithProviderID); err != nil {
					slog.Error("检查模型关联提供商自动关闭失败", "error", err, "model_with_provider_id", modelWithProviderID)
				}
			}()
		}
		return log.ID, nil
	}

	return 0, errors.New("failed to generate unique chat log uuid")
}

func BuildHeaders(source http.Header, withHeader bool, customHeaders map[string]string, stream bool) http.Header {
	return runtimesvc.BuildHeaders(source, withHeader, customHeaders, stream)
}

type ProvidersWithMeta struct {
	ModelWithProviderMap map[uint]models.ModelWithProvider
	WeightItems          map[uint]int
	ProviderMap          map[uint]models.Provider
	MaxRetry             int
	TimeOut              int
	IOLog                bool
	Strategy             string // 负载均衡策略
	Breaker              bool   // 是否开启熔断
}

func ProvidersWithMetaBymodelsName(ctx context.Context, logStyle string, endpoint string, before Before) (*ProvidersWithMeta, error) {
	if err := RestoreExpiredAutoDisabledModelProviders(ctx); err != nil {
		slog.Warn("恢复已到期的模型关联提供商失败", "error", err)
	}

	model, err := gorm.G[models.Model](models.DB).Where("name = ?", before.Model).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err := SaveChatLog(ctx, models.ChatLog{
				Name:   before.Model,
				Status: "error",
				Style:  logStyle,
				Error:  err.Error(),
			}); err != nil {
				return nil, err
			}
			return nil, errors.New("not found model " + before.Model)
		}
		return nil, err
	}
	if model.Status == 0 {
		if _, err := SaveChatLog(ctx, models.ChatLog{
			Name:   before.Model,
			Status: "error",
			Style:  logStyle,
			Error:  "model disabled",
		}); err != nil {
			return nil, err
		}
		return nil, errors.New("model disabled " + before.Model)
	}

	// model_with_providers.status 在数据库中是 0/1（int）
	modelWithProviderChain := gorm.G[models.ModelWithProvider](models.DB).Where("model_id = ?", model.ID).Where("status = ?", 1)

	modelWithProviders, err := modelWithProviderChain.Find(ctx)
	if err != nil {
		return nil, err
	}

	if len(modelWithProviders) == 0 {
		return nil, errors.New("not provider for model " + before.Model)
	}

	modelWithProviderMap := lo.KeyBy(modelWithProviders, func(mp models.ModelWithProvider) uint { return mp.ID })

	providerQuery := gorm.G[models.Provider](models.DB).
		Where("id IN ?", lo.Map(modelWithProviders, func(mp models.ModelWithProvider, _ int) uint { return mp.ProviderID }))
	providers, err := providerQuery.Find(ctx)
	if err != nil {
		return nil, err
	}

	providerMap := lo.KeyBy(providers, func(p models.Provider) uint { return p.ID })

	weightItems := make(map[uint]int)
	for _, mp := range modelWithProviders {
		provider, ok := providerMap[mp.ProviderID]
		if !ok {
			continue
		}
		if !models.ProviderSupportsEndpoint([]string(provider.Capabilities), endpoint) {
			continue
		}
		weightItems[mp.ID] = mp.Weight
	}

	if len(weightItems) == 0 {
		return nil, errors.New("not provider for model " + before.Model)
	}

	// IOLog 和 Breaker 现在是 int 类型(0/1)
	ioLog := model.IOLog == 1
	breaker := model.Breaker == 1

	return &ProvidersWithMeta{
		ModelWithProviderMap: modelWithProviderMap,
		WeightItems:          weightItems,
		ProviderMap:          providerMap,
		MaxRetry:             model.MaxRetry,
		TimeOut:              model.TimeOut,
		IOLog:                ioLog,
		Strategy:             model.Strategy,
		Breaker:              breaker,
	}, nil
}
