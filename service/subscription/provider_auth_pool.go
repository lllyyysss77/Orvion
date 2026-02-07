package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/racio/orvion/consts"
)

var (
	openAIAuthRoundRobinMu sync.Mutex
	openAIAuthRoundRobin   = make(map[uint]uint64)
)

const codexAuthsFixedBaseURL = "https://chatgpt.com/backend-api/codex"
const iflowAuthsFixedBaseURL = "https://apis.iflow.cn/v1"

// ResolveProviderConfigForRequest 根据 provider 配置动态注入请求所需凭据。
// 规则：
// 1) 仅 openai/codex/codex-auths/iflow/iflow-auths 类型处理 auth_files；
// 2) 未配置 auth_files 时沿用原始 config（兼容已有 api_key 流程）；
// 3) auth 类型（codex-auths/iflow/iflow-auths）在未显式配置 auth_files 时，自动从订阅池取可用凭据。
// 4) 配置了 auth_files 时，按 provider 维度轮询选取可用 token 覆盖 api_key。
func ResolveProviderConfigForRequest(providerID uint, providerType, providerConfig string) (string, error) {
	providerType = strings.TrimSpace(providerType)
	if providerType == consts.StyleIFlow {
		return buildIFlowDynamicConfig()
	}
	if providerType != consts.StyleOpenAI &&
		providerType != consts.StyleOpenAIRes &&
		providerType != consts.StyleCodexAuths &&
		providerType != consts.StyleIFlow &&
		providerType != consts.StyleIFlowAuths {
		return providerConfig, nil
	}

	raw := strings.TrimSpace(providerConfig)
	if raw == "" {
		switch providerType {
		case consts.StyleCodexAuths:
			return buildCodexDynamicConfig()
		case consts.StyleIFlowAuths:
			return buildIFlowDynamicConfig()
		}
		return "", errors.New("provider 配置为空")
	}

	configObj := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &configObj); err != nil {
		return "", fmt.Errorf("解析 provider 配置失败: %w", err)
	}

	authFiles := parseAuthFiles(configObj["auth_files"])
	if len(authFiles) == 0 {
		switch providerType {
		case consts.StyleCodexAuths:
			return buildCodexDynamicConfig()
		case consts.StyleIFlowAuths:
			return buildIFlowDynamicConfig()
		}
		return providerConfig, nil
	}

	token, _, err := pickRoundRobinAuthToken(providerID, authFiles)
	if err != nil {
		return "", err
	}

	configObj["api_key"] = token
	if providerType == consts.StyleCodexAuths {
		// codex-auths 统一走 chatgpt backend codex responses 链路。
		configObj["base_url"] = codexAuthsFixedBaseURL
	} else if providerType == consts.StyleIFlowAuths {
		// iflow-auths 统一走 iFlow OpenAI 兼容链路。
		configObj["base_url"] = iflowAuthsFixedBaseURL
	}
	encoded, err := json.Marshal(configObj)
	if err != nil {
		return "", fmt.Errorf("序列化 provider 配置失败: %w", err)
	}
	return string(encoded), nil
}

func buildCodexDynamicConfig() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	credential, err := GetCodexSubscriptionManager().ResolveRequestCredential(ctx, "")
	if err != nil {
		return "", fmt.Errorf("解析 codex 订阅失败: %w", err)
	}

	configObj := map[string]any{
		"base_url": codexAuthsFixedBaseURL,
		"api_key":  credential.AccessToken,
	}
	encoded, err := json.Marshal(configObj)
	if err != nil {
		return "", fmt.Errorf("序列化 codex provider 配置失败: %w", err)
	}
	return string(encoded), nil
}

func buildIFlowDynamicConfig() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	credential, err := GetIFlowSubscriptionManager().ResolveRequestCredential(ctx, "")
	if err != nil {
		return "", fmt.Errorf("解析 iflow 订阅失败: %w", err)
	}

	configObj := map[string]any{
		"base_url": iflowAuthsFixedBaseURL,
		"api_key":  credential.APIKey,
	}
	encoded, err := json.Marshal(configObj)
	if err != nil {
		return "", fmt.Errorf("序列化 iflow provider 配置失败: %w", err)
	}
	return string(encoded), nil
}

func parseAuthFiles(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}

	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func pickRoundRobinAuthToken(providerID uint, authFiles []string) (token string, fileName string, err error) {
	if len(authFiles) == 0 {
		return "", "", errors.New("未配置 auth_files")
	}

	start := nextAuthFileIndex(providerID, len(authFiles))
	errs := make([]string, 0, len(authFiles))

	for i := 0; i < len(authFiles); i++ {
		idx := (start + i) % len(authFiles)
		fileName = authFiles[idx]
		token, loadErr := loadAuthTokenFromFile(fileName)
		if loadErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", fileName, loadErr))
			continue
		}
		return token, fileName, nil
	}

	return "", "", fmt.Errorf("auth_files 均不可用: %s", strings.Join(errs, "; "))
}

func nextAuthFileIndex(providerID uint, size int) int {
	if size <= 1 {
		return 0
	}
	openAIAuthRoundRobinMu.Lock()
	defer openAIAuthRoundRobinMu.Unlock()

	current := openAIAuthRoundRobin[providerID]
	openAIAuthRoundRobin[providerID] = current + 1
	return int(current % uint64(size))
}

func loadAuthTokenFromFile(fileName string) (string, error) {
	fileName = strings.TrimSpace(fileName)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch {
	case codexCredentialIDPattern.MatchString(fileName):
		manager := GetCodexSubscriptionManager()
		token, err := manager.GetAccessToken(ctx, fileName)
		if err != nil {
			return "", fmt.Errorf("读取 codex 凭据失败: %w", err)
		}
		return token, nil
	case iflowCredentialIDPattern.MatchString(fileName):
		manager := GetIFlowSubscriptionManager()
		token, err := manager.GetAPIKey(ctx, fileName)
		if err != nil {
			return "", fmt.Errorf("读取 iflow 凭据失败: %w", err)
		}
		return token, nil
	default:
		return "", errors.New("凭据文件名不合法")
	}
}
