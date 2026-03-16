package subscription

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultIFlowSubscriptionDir = "data/iflow-auths"
	defaultIFlowAPIKeyEndpoint  = "https://platform.iflow.cn/api/openapi/apikey"
	defaultIFlowAPIBaseURL      = "https://apis.iflow.cn/v1"
	defaultIFlowOAuthAuthURL    = "https://iflow.cn/oauth"
	defaultIFlowOAuthTokenURL   = "https://iflow.cn/oauth/token"
	defaultIFlowOAuthUserInfo   = "https://iflow.cn/api/oauth/getUserInfo"
	defaultIFlowOAuthClientID   = "10009311001"
	defaultIFlowOAuthSecret     = "4Z3YjXycVsQvyGF1etiNlIBB4RsqSDtW"
	defaultIFlowOAuthCallback   = "/iflow/callback"
	iflowOAuthStateLength       = 40
	iflowSessionTTL             = 10 * time.Minute
	iflowSessionRetention       = 30 * time.Minute
	iflowCookieRefreshLead      = 48 * time.Hour
)

var (
	ErrIFlowSubscriptionNotFound = errors.New("iflow 订阅凭据不存在")
	ErrIFlowDuplicateBXAuth      = errors.New("已存在相同 BXAuth 的 iflow 订阅")
	ErrIFlowInvalidState         = errors.New("非法 state 参数")
	ErrIFlowSessionNotFound      = errors.New("授权会话不存在")
	ErrIFlowSessionNotPending    = errors.New("授权会话已结束")

	iflowCredentialIDPattern = regexp.MustCompile(`^iflow-[A-Za-z0-9._@-]+\.json$`)
	iflowCleanerPattern      = regexp.MustCompile(`[^a-zA-Z0-9._@-]+`)
	iflowStatePattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

	// 参考 CLIProxyAPIPlus: iFlow 可用模型由本地静态列表维护，
	// 通过订阅文件存在性校验后返回，避免依赖上游模型接口波动。
	iflowOpenAIModels = []IFlowModel{
		{ID: "tstars2.0", Object: "model", Created: 1746489600, OwnedBy: "iflow", Type: "iflow", DisplayName: "TStars-2.0"},
		{ID: "qwen3-coder-plus", Object: "model", Created: 1753228800, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-Coder-Plus"},
		{ID: "qwen3-max", Object: "model", Created: 1758672000, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-Max"},
		{ID: "qwen3-vl-plus", Object: "model", Created: 1758672000, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-VL-Plus"},
		{ID: "qwen3-max-preview", Object: "model", Created: 1757030400, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-Max-Preview"},
		{ID: "kimi-k2-0905", Object: "model", Created: 1757030400, OwnedBy: "iflow", Type: "iflow", DisplayName: "Kimi-K2-Instruct-0905"},
		{ID: "glm-5", Object: "model", Created: 1759190400, OwnedBy: "iflow", Type: "iflow", DisplayName: "GLM-5"},
		{ID: "glm-4.7", Object: "model", Created: 1766448000, OwnedBy: "iflow", Type: "iflow", DisplayName: "GLM-4.7"},
		{ID: "kimi-k2", Object: "model", Created: 1752192000, OwnedBy: "iflow", Type: "iflow", DisplayName: "Kimi-K2"},
		{ID: "kimi-k2-thinking", Object: "model", Created: 1762387200, OwnedBy: "iflow", Type: "iflow", DisplayName: "Kimi-K2-Thinking"},
		{ID: "deepseek-v3.2-chat", Object: "model", Created: 1764576000, OwnedBy: "iflow", Type: "iflow", DisplayName: "DeepSeek-V3.2"},
		{ID: "deepseek-v3.2-reasoner", Object: "model", Created: 1764576000, OwnedBy: "iflow", Type: "iflow", DisplayName: "DeepSeek-V3.2"},
		{ID: "deepseek-v3.2", Object: "model", Created: 1759104000, OwnedBy: "iflow", Type: "iflow", DisplayName: "DeepSeek-V3.2-Exp"},
		{ID: "deepseek-v3.1", Object: "model", Created: 1756339200, OwnedBy: "iflow", Type: "iflow", DisplayName: "DeepSeek-V3.1-Terminus"},
		{ID: "deepseek-r1", Object: "model", Created: 1737331200, OwnedBy: "iflow", Type: "iflow", DisplayName: "DeepSeek-R1"},
		{ID: "deepseek-v3", Object: "model", Created: 1734307200, OwnedBy: "iflow", Type: "iflow", DisplayName: "DeepSeek-V3-671B"},
		{ID: "qwen3-32b", Object: "model", Created: 1747094400, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-32B"},
		{ID: "qwen3-235b-a22b-thinking-2507", Object: "model", Created: 1753401600, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-235B-A22B-Thinking"},
		{ID: "qwen3-235b-a22b-instruct", Object: "model", Created: 1753401600, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-235B-A22B-Instruct"},
		{ID: "qwen3-235b", Object: "model", Created: 1753401600, OwnedBy: "iflow", Type: "iflow", DisplayName: "Qwen3-235B-A22B"},
		{ID: "minimax-m2", Object: "model", Created: 1758672000, OwnedBy: "iflow", Type: "iflow", DisplayName: "MiniMax-M2"},
		{ID: "minimax-m2.1", Object: "model", Created: 1766448000, OwnedBy: "iflow", Type: "iflow", DisplayName: "MiniMax-M2.1"},
		{ID: "iflow-rome-30ba3b", Object: "model", Created: 1736899200, OwnedBy: "iflow", Type: "iflow", DisplayName: "iFlow-ROME"},
		{ID: "kimi-k2.5", Object: "model", Created: 1769443200, OwnedBy: "iflow", Type: "iflow", DisplayName: "Kimi-K2.5"},
	}
)

type IFlowSubscription struct {
	ID          string    `json:"id"`
	FileName    string    `json:"file_name"`
	Email       string    `json:"email,omitempty"`
	Expired     string    `json:"expired,omitempty"`
	LastRefresh string    `json:"last_refresh,omitempty"`
	Type        string    `json:"type,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type iflowCredentialRecord struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
	Expired      string `json:"expired,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	Email        string `json:"email,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Cookie       string `json:"cookie,omitempty"`
	Type         string `json:"type,omitempty"`
}

type iflowAPIKeyResponse struct {
	Success bool         `json:"success"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Data    iflowKeyData `json:"data"`
}

type iflowKeyData struct {
	HasExpired bool   `json:"hasExpired"`
	ExpireTime string `json:"expireTime"`
	Name       string `json:"name"`
	APIKey     string `json:"apiKey"`
	APIKeyMask string `json:"apiKeyMask"`
}

type iflowRefreshRequest struct {
	Name string `json:"name"`
}

type IFlowRequestCredential struct {
	SubscriptionID string `json:"subscription_id"`
	APIKey         string `json:"api_key"`
	Email          string `json:"email,omitempty"`
}

type IFlowModel struct {
	ID          string `json:"id"`
	Object      string `json:"object,omitempty"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type IFlowOAuthStartResult struct {
	State     string    `json:"state"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type IFlowOAuthStatusResult struct {
	State      string             `json:"state"`
	Status     string             `json:"status"` // wait / ok / error
	Message    string             `json:"message,omitempty"`
	Credential *IFlowSubscription `json:"credential,omitempty"`
}

type iflowOAuthSession struct {
	State        string
	RedirectURI  string
	Status       string // pending / ok / error
	ErrorMessage string
	Credential   *IFlowSubscription
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

type iflowOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

type iflowOAuthUserInfoResponse struct {
	Success bool `json:"success"`
	Data    struct {
		APIKey string `json:"apiKey"`
		Email  string `json:"email"`
		Phone  string `json:"phone"`
	} `json:"data"`
}

type IFlowSubscriptionManager struct {
	mu           sync.RWMutex
	credentialMu sync.Mutex
	baseDir      string
	authDir      string
	sessions     map[string]*iflowOAuthSession
	client       *http.Client
}

var (
	iflowSubscriptionManager     *IFlowSubscriptionManager
	iflowSubscriptionManagerOnce sync.Once
)

func GetIFlowSubscriptionManager() *IFlowSubscriptionManager {
	iflowSubscriptionManagerOnce.Do(func() {
		baseDir := strings.TrimSpace(os.Getenv("IFLOW_SUBSCRIPTION_DIR"))
		if baseDir == "" {
			baseDir = defaultIFlowSubscriptionDir
		}

		manager, err := NewIFlowSubscriptionManager(baseDir)
		if err != nil {
			manager, err = NewIFlowSubscriptionManager(defaultIFlowSubscriptionDir)
			if err != nil {
				manager, err = NewIFlowSubscriptionManager(filepath.Join(os.TempDir(), "llmio-iflow-auths"))
				if err != nil {
					manager = &IFlowSubscriptionManager{
						baseDir:  "",
						authDir:  "",
						sessions: make(map[string]*iflowOAuthSession),
						client: &http.Client{
							Timeout: 20 * time.Second,
						},
					}
				}
			}
		}

		iflowSubscriptionManager = manager
	})
	return iflowSubscriptionManager
}

func NewIFlowSubscriptionManager(baseDir string) (*IFlowSubscriptionManager, error) {
	cleanBaseDir := strings.TrimSpace(baseDir)
	if cleanBaseDir == "" {
		cleanBaseDir = defaultIFlowSubscriptionDir
	}
	authDir := filepath.Join(cleanBaseDir, "auths")

	if err := os.MkdirAll(cleanBaseDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return nil, err
	}

	return &IFlowSubscriptionManager{
		baseDir:  cleanBaseDir,
		authDir:  authDir,
		sessions: make(map[string]*iflowOAuthSession),
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}, nil
}

func GetIFlowAPIBaseURL() string {
	baseURL := strings.TrimSpace(os.Getenv("IFLOW_API_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultIFlowAPIBaseURL
	}
	return strings.TrimRight(baseURL, "/")
}

func (m *IFlowSubscriptionManager) StartOAuthSession(redirectURI string) (*IFlowOAuthStartResult, error) {
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return nil, errors.New("iflow oauth redirect_uri 不能为空")
	}

	state, err := generateRandomString(iflowOAuthStateLength)
	if err != nil {
		return nil, err
	}

	authURL, err := buildIFlowAuthorizeURL(getIFlowOAuthClientID(), redirectURI, state)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &iflowOAuthSession{
		State:       state,
		RedirectURI: redirectURI,
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(iflowSessionTTL),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupSessionsLocked(time.Now())
	m.sessions[state] = session

	return &IFlowOAuthStartResult{
		State:     state,
		AuthURL:   authURL,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

func (m *IFlowSubscriptionManager) PersistOAuthCallback(code, state, authErr, authErrDesc string) error {
	state = strings.TrimSpace(state)
	if !iflowStatePattern.MatchString(state) {
		return ErrIFlowInvalidState
	}

	m.mu.RLock()
	session, ok := m.sessions[state]
	if !ok {
		m.mu.RUnlock()
		return ErrIFlowSessionNotFound
	}
	if session.Status != "pending" {
		m.mu.RUnlock()
		return ErrIFlowSessionNotPending
	}
	if time.Now().After(session.ExpiresAt) {
		m.mu.RUnlock()
		m.setSessionError(state, "授权会话已过期，请重新发起")
		return ErrIFlowSessionNotPending
	}
	redirectURI := session.RedirectURI
	m.mu.RUnlock()

	if strings.TrimSpace(authErr) != "" {
		message := strings.TrimSpace(authErr)
		if strings.TrimSpace(authErrDesc) != "" {
			message = message + ": " + strings.TrimSpace(authErrDesc)
		}
		m.setSessionError(state, message)
		return errors.New(message)
	}

	code = strings.TrimSpace(code)
	if code == "" {
		m.setSessionError(state, "回调缺少 code 参数")
		return errors.New("回调缺少 code 参数")
	}

	tokenResp, err := m.exchangeCodeForTokens(context.Background(), code, redirectURI)
	if err != nil {
		m.setSessionError(state, "换取 iflow token 失败: "+err.Error())
		return err
	}

	userInfo, err := m.fetchOAuthUserInfo(context.Background(), tokenResp.AccessToken)
	if err != nil {
		m.setSessionError(state, "获取 iflow 用户信息失败: "+err.Error())
		return err
	}

	apiKey := strings.TrimSpace(userInfo.Data.APIKey)
	if apiKey == "" {
		m.setSessionError(state, "iflow 返回 api key 为空")
		return errors.New("iflow 返回 api key 为空")
	}

	email := strings.TrimSpace(userInfo.Data.Email)
	if email == "" {
		email = strings.TrimSpace(userInfo.Data.Phone)
	}
	if email == "" {
		m.setSessionError(state, "iflow 返回账号标识为空")
		return errors.New("iflow 返回账号标识为空")
	}

	now := time.Now()
	record := iflowCredentialRecord{
		AccessToken:  strings.TrimSpace(tokenResp.AccessToken),
		RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
		LastRefresh:  now.Format(time.RFC3339),
		Expired:      now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339),
		APIKey:       apiKey,
		Email:        email,
		TokenType:    strings.TrimSpace(tokenResp.TokenType),
		Scope:        strings.TrimSpace(tokenResp.Scope),
		Type:         "iflow",
	}
	fileName := buildIFlowCredentialFileName(email)

	m.credentialMu.Lock()
	path, err := m.nextCredentialPath(fileName)
	if err == nil {
		err = writeJSON0600(path, record)
	}
	m.credentialMu.Unlock()
	if err != nil {
		m.setSessionError(state, "保存 iflow 凭据失败: "+err.Error())
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		m.setSessionError(state, "读取 iflow 凭据文件失败: "+err.Error())
		return err
	}

	sub := &IFlowSubscription{
		ID:          filepath.Base(path),
		FileName:    filepath.Base(path),
		Email:       record.Email,
		Expired:     record.Expired,
		LastRefresh: record.LastRefresh,
		Type:        "iflow",
		CreatedAt:   info.ModTime(),
		UpdatedAt:   info.ModTime(),
	}
	m.completeSession(state, sub)
	return nil
}

func (m *IFlowSubscriptionManager) GetOAuthStatus(state string) (*IFlowOAuthStatusResult, error) {
	state = strings.TrimSpace(state)
	if !iflowStatePattern.MatchString(state) {
		return nil, ErrIFlowInvalidState
	}

	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupSessionsLocked(now)

	session, ok := m.sessions[state]
	if !ok {
		return nil, ErrIFlowSessionNotFound
	}
	if session.Status == "pending" && now.After(session.ExpiresAt) {
		session.Status = "error"
		session.ErrorMessage = "授权会话已过期，请重新发起"
		session.UpdatedAt = now
	}

	result := &IFlowOAuthStatusResult{
		State: state,
	}
	switch session.Status {
	case "ok":
		result.Status = "ok"
		result.Credential = session.Credential
	case "error":
		result.Status = "error"
		result.Message = session.ErrorMessage
	default:
		result.Status = "wait"
	}
	return result, nil
}

func (m *IFlowSubscriptionManager) ListSubscriptions() ([]IFlowSubscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.authDir == "" {
		return []IFlowSubscription{}, nil
	}

	files, err := collectCredentialFiles(m.authDir, iflowCredentialIDPattern)
	if err != nil {
		return nil, err
	}

	subs := make([]IFlowSubscription, 0, len(files))
	for _, file := range files {
		content, err := os.ReadFile(file.Path)
		if err != nil {
			continue
		}

		var record iflowCredentialRecord
		if err := json.Unmarshal(content, &record); err != nil {
			continue
		}

		sub := IFlowSubscription{
			ID:          file.Name,
			FileName:    file.Name,
			Email:       strings.TrimSpace(record.Email),
			Expired:     strings.TrimSpace(record.Expired),
			LastRefresh: strings.TrimSpace(record.LastRefresh),
			Type:        strings.TrimSpace(record.Type),
			CreatedAt:   file.ModTime,
			UpdatedAt:   file.ModTime,
		}
		subs = append(subs, sub)
	}

	sort.SliceStable(subs, func(i, j int) bool {
		if subs[i].UpdatedAt.Equal(subs[j].UpdatedAt) {
			return subs[i].ID < subs[j].ID
		}
		return subs[i].UpdatedAt.After(subs[j].UpdatedAt)
	})

	return subs, nil
}

func (m *IFlowSubscriptionManager) DeleteSubscription(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrIFlowSubscriptionNotFound
	}
	if !iflowCredentialIDPattern.MatchString(id) {
		return ErrIFlowSubscriptionNotFound
	}
	if filepath.Base(id) != id {
		return ErrIFlowSubscriptionNotFound
	}

	m.credentialMu.Lock()
	defer m.credentialMu.Unlock()

	path, err := resolveCredentialPath(m.authDir, id, iflowCredentialIDPattern)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrIFlowSubscriptionNotFound
		}
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrIFlowSubscriptionNotFound
		}
		return err
	}
	return nil
}

func (m *IFlowSubscriptionManager) AddSubscriptionByCookie(ctx context.Context, rawCookie string) (*IFlowSubscription, error) {
	cookie, err := normalizeIFlowCookie(rawCookie)
	if err != nil {
		return nil, err
	}
	bxAuth := extractIFlowBXAuth(cookie)
	if bxAuth == "" {
		return nil, errors.New("cookie 缺少 BXAuth 字段")
	}

	if existing, err := m.findSubscriptionByBXAuth(bxAuth); err != nil {
		return nil, err
	} else if existing != "" {
		return nil, ErrIFlowDuplicateBXAuth
	}

	keyInfo, err := m.fetchAPIKeyInfo(ctx, cookie)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(keyInfo.Name)
	if name == "" {
		return nil, errors.New("iflow 返回的账户标识为空")
	}

	refreshed, err := m.refreshAPIKey(ctx, cookie, name)
	if err != nil {
		return nil, err
	}

	apiKey := strings.TrimSpace(refreshed.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(keyInfo.APIKey)
	}
	if apiKey == "" {
		return nil, errors.New("iflow 返回的 api key 为空")
	}

	expired := strings.TrimSpace(refreshed.ExpireTime)
	if expired == "" {
		expired = strings.TrimSpace(keyInfo.ExpireTime)
	}

	now := time.Now()
	record := iflowCredentialRecord{
		APIKey:      apiKey,
		Email:       name,
		Expired:     expired,
		LastRefresh: now.Format(time.RFC3339),
		Cookie:      buildIFlowBXAuthCookie(bxAuth),
		Type:        "iflow",
	}

	fileName := buildIFlowCredentialFileName(name)

	m.credentialMu.Lock()
	defer m.credentialMu.Unlock()

	if existing, err := m.findSubscriptionByBXAuth(bxAuth); err != nil {
		return nil, err
	} else if existing != "" {
		return nil, ErrIFlowDuplicateBXAuth
	}

	path, err := m.nextCredentialPath(fileName)
	if err != nil {
		return nil, err
	}
	if err := writeJSON0600(path, record); err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	result := &IFlowSubscription{
		ID:          filepath.Base(path),
		FileName:    filepath.Base(path),
		Email:       name,
		Expired:     expired,
		LastRefresh: record.LastRefresh,
		Type:        record.Type,
		CreatedAt:   info.ModTime(),
		UpdatedAt:   info.ModTime(),
	}
	return result, nil
}

func (m *IFlowSubscriptionManager) ResolveRequestCredential(ctx context.Context, preferredSubscriptionID string) (*IFlowRequestCredential, error) {
	preferredSubscriptionID = strings.TrimSpace(preferredSubscriptionID)
	if preferredSubscriptionID != "" {
		apiKey, err := m.GetAPIKey(ctx, preferredSubscriptionID)
		if err != nil {
			return nil, err
		}
		record, err := m.readCredentialRecord(preferredSubscriptionID)
		if err != nil {
			return nil, err
		}
		return &IFlowRequestCredential{
			SubscriptionID: preferredSubscriptionID,
			APIKey:         apiKey,
			Email:          strings.TrimSpace(record.Email),
		}, nil
	}

	subs, err := m.ListSubscriptions()
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, ErrIFlowSubscriptionNotFound
	}

	var errs []string
	for _, sub := range subs {
		apiKey, keyErr := m.GetAPIKey(ctx, sub.ID)
		if keyErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sub.ID, keyErr))
			continue
		}
		record, recErr := m.readCredentialRecord(sub.ID)
		if recErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", sub.ID, recErr))
			continue
		}
		return &IFlowRequestCredential{
			SubscriptionID: sub.ID,
			APIKey:         apiKey,
			Email:          strings.TrimSpace(record.Email),
		}, nil
	}

	if len(errs) > 0 {
		return nil, errors.New("iflow 订阅均不可用: " + strings.Join(errs, "; "))
	}
	return nil, ErrIFlowSubscriptionNotFound
}

func (m *IFlowSubscriptionManager) ListAvailableModels(ctx context.Context, subscriptionID string) ([]IFlowModel, error) {
	subscriptionID = strings.TrimSpace(subscriptionID)
	if subscriptionID == "" {
		return nil, ErrIFlowSubscriptionNotFound
	}
	if _, err := m.readCredentialRecord(subscriptionID); err != nil {
		return nil, err
	}
	_ = ctx

	list := make([]IFlowModel, 0, len(iflowOpenAIModels))
	list = append(list, iflowOpenAIModels...)
	return list, nil
}

func (m *IFlowSubscriptionManager) GetAPIKey(ctx context.Context, subscriptionID string) (string, error) {
	m.credentialMu.Lock()
	defer m.credentialMu.Unlock()

	record, path, err := m.readCredentialRecordWithPath(subscriptionID)
	if err != nil {
		return "", err
	}

	updated, changed, err := m.refreshCredentialRecordIfNeeded(ctx, record)
	if err != nil {
		return "", err
	}
	if changed {
		if err := writeJSON0600(path, updated); err != nil {
			return "", err
		}
		record = updated
	}

	apiKey := strings.TrimSpace(record.APIKey)
	if apiKey == "" {
		return "", errors.New("iflow 订阅凭据缺少 api_key")
	}
	return apiKey, nil
}

func (m *IFlowSubscriptionManager) findSubscriptionByBXAuth(bxAuth string) (string, error) {
	bxAuth = strings.TrimSpace(bxAuth)
	if bxAuth == "" {
		return "", nil
	}

	subs, err := m.ListSubscriptions()
	if err != nil {
		return "", err
	}
	for _, sub := range subs {
		record, err := m.readCredentialRecord(sub.ID)
		if err != nil {
			continue
		}
		if extractIFlowBXAuth(record.Cookie) == bxAuth {
			return sub.ID, nil
		}
	}
	return "", nil
}

func (m *IFlowSubscriptionManager) refreshCredentialRecordIfNeeded(ctx context.Context, record iflowCredentialRecord) (iflowCredentialRecord, bool, error) {
	if !shouldRefreshIFlowAPIKey(record.Expired, time.Now()) {
		return record, false, nil
	}

	cookie := strings.TrimSpace(record.Cookie)
	email := strings.TrimSpace(record.Email)
	// 优先沿用 cookie 刷新链路，兼容手动添加 cookie 的订阅。
	if cookie != "" && email != "" {
		keyData, err := m.refreshAPIKey(ctx, cookie, email)
		if err != nil {
			return record, false, err
		}
		if strings.TrimSpace(keyData.APIKey) == "" {
			return record, false, errors.New("iflow 刷新后 api key 为空")
		}

		record.APIKey = strings.TrimSpace(keyData.APIKey)
		if strings.TrimSpace(keyData.ExpireTime) != "" {
			record.Expired = strings.TrimSpace(keyData.ExpireTime)
		}
		record.LastRefresh = time.Now().Format(time.RFC3339)
		if strings.TrimSpace(record.Type) == "" {
			record.Type = "iflow"
		}
		return record, true, nil
	}

	// OAuth 订阅没有 cookie 时，使用 refresh_token 刷新 access_token，并回填最新 api_key。
	refreshToken := strings.TrimSpace(record.RefreshToken)
	if refreshToken == "" {
		return record, false, errors.New("iflow 订阅缺少可用刷新信息（cookie/refresh_token）")
	}

	tokenResp, err := m.exchangeRefreshTokenForTokens(ctx, refreshToken)
	if err != nil {
		return record, false, fmt.Errorf("iflow oauth 刷新失败: %w", err)
	}

	userInfo, err := m.fetchOAuthUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return record, false, fmt.Errorf("iflow oauth 刷新后获取用户信息失败: %w", err)
	}
	apiKey := strings.TrimSpace(userInfo.Data.APIKey)
	if apiKey == "" {
		return record, false, errors.New("iflow 刷新后 api key 为空")
	}

	now := time.Now()
	record.AccessToken = strings.TrimSpace(tokenResp.AccessToken)
	if nextRefreshToken := strings.TrimSpace(tokenResp.RefreshToken); nextRefreshToken != "" {
		record.RefreshToken = nextRefreshToken
	}
	record.APIKey = apiKey
	refreshedEmail := strings.TrimSpace(userInfo.Data.Email)
	if refreshedEmail == "" {
		refreshedEmail = strings.TrimSpace(userInfo.Data.Phone)
	}
	if refreshedEmail != "" {
		record.Email = refreshedEmail
	}
	if tokenResp.ExpiresIn > 0 {
		record.Expired = now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if tokenType := strings.TrimSpace(tokenResp.TokenType); tokenType != "" {
		record.TokenType = tokenType
	}
	if scope := strings.TrimSpace(tokenResp.Scope); scope != "" {
		record.Scope = scope
	}
	record.LastRefresh = now.Format(time.RFC3339)
	if strings.TrimSpace(record.Type) == "" {
		record.Type = "iflow"
	}
	return record, true, nil
}

func shouldRefreshIFlowAPIKey(expired string, now time.Time) bool {
	expired = strings.TrimSpace(expired)
	if expired == "" {
		// 过期时间缺失时保守刷新，避免静默失效。
		return true
	}

	expireAt, ok := parseIFlowExpireTime(expired)
	if !ok {
		// 过期时间无法解析时兜底刷新。
		return true
	}

	if now.After(expireAt) {
		return true
	}
	return expireAt.Sub(now) <= iflowCookieRefreshLead
}

func parseIFlowExpireTime(expired string) (time.Time, bool) {
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, expired, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func (m *IFlowSubscriptionManager) fetchAPIKeyInfo(ctx context.Context, cookie string) (*iflowKeyData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, defaultIFlowAPIKeyEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 iflow apikey 请求失败: %w", err)
	}
	m.setIFlowCookieHeaders(req, cookie)

	body, err := m.doIFlowAPIKeyRequest(req)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(body.Data.APIKey) == "" && strings.TrimSpace(body.Data.APIKeyMask) != "" {
		body.Data.APIKey = strings.TrimSpace(body.Data.APIKeyMask)
	}
	return &body.Data, nil
}

func (m *IFlowSubscriptionManager) refreshAPIKey(ctx context.Context, cookie, name string) (*iflowKeyData, error) {
	payload, err := json.Marshal(iflowRefreshRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("序列化 iflow 刷新请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, defaultIFlowAPIKeyEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建 iflow 刷新请求失败: %w", err)
	}
	m.setIFlowCookieHeaders(req, cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://platform.iflow.cn")
	req.Header.Set("Referer", "https://platform.iflow.cn/")

	body, err := m.doIFlowAPIKeyRequest(req)
	if err != nil {
		return nil, err
	}
	return &body.Data, nil
}

func (m *IFlowSubscriptionManager) setIFlowCookieHeaders(req *http.Request, cookie string) {
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
}

func (m *IFlowSubscriptionManager) doIFlowAPIKeyRequest(req *http.Request) (*iflowAPIKeyResponse, error) {
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 iflow apikey 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 iflow apikey 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iflow apikey 请求失败(%d): %s", resp.StatusCode, strings.TrimSpace(string(content)))
	}

	var parsed iflowAPIKeyResponse
	if err := json.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("解析 iflow apikey 响应失败: %w", err)
	}
	if !parsed.Success {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = "iflow apikey 接口返回失败"
		}
		return nil, errors.New(msg)
	}
	return &parsed, nil
}

func (m *IFlowSubscriptionManager) readCredentialRecord(id string) (iflowCredentialRecord, error) {
	record, _, err := m.readCredentialRecordWithPath(id)
	return record, err
}

func (m *IFlowSubscriptionManager) readCredentialRecordWithPath(id string) (iflowCredentialRecord, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return iflowCredentialRecord{}, "", ErrIFlowSubscriptionNotFound
	}
	if !iflowCredentialIDPattern.MatchString(id) {
		return iflowCredentialRecord{}, "", ErrIFlowSubscriptionNotFound
	}
	if filepath.Base(id) != id {
		return iflowCredentialRecord{}, "", ErrIFlowSubscriptionNotFound
	}

	path, err := resolveCredentialPath(m.authDir, id, iflowCredentialIDPattern)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return iflowCredentialRecord{}, "", ErrIFlowSubscriptionNotFound
		}
		return iflowCredentialRecord{}, "", err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return iflowCredentialRecord{}, "", ErrIFlowSubscriptionNotFound
		}
		return iflowCredentialRecord{}, "", err
	}

	var record iflowCredentialRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return iflowCredentialRecord{}, "", err
	}
	return record, path, nil
}

func (m *IFlowSubscriptionManager) nextCredentialPath(fileName string) (string, error) {
	if m.authDir == "" {
		return "", errors.New("iflow 凭据目录不可用")
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return "", errors.New("iflow 凭据文件名为空")
	}
	path := filepath.Join(m.authDir, fileName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	return filepath.Join(m.authDir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixMilli(), ext)), nil
}

func normalizeIFlowCookie(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("cookie 不能为空")
	}

	combined := strings.Join(strings.Fields(trimmed), " ")
	if !strings.HasSuffix(combined, ";") {
		combined += ";"
	}
	if extractIFlowBXAuth(combined) == "" {
		return "", errors.New("cookie 缺少 BXAuth 字段")
	}
	return combined, nil
}

func extractIFlowBXAuth(cookie string) string {
	parts := strings.Split(cookie, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "BXAuth=") {
			return strings.TrimSpace(strings.TrimPrefix(part, "BXAuth="))
		}
	}
	return ""
}

func buildIFlowBXAuthCookie(bxAuth string) string {
	bxAuth = strings.TrimSpace(bxAuth)
	if bxAuth == "" {
		return ""
	}
	return "BXAuth=" + bxAuth + ";"
}

func buildIFlowCredentialFileName(identifier string) string {
	value := strings.TrimSpace(identifier)
	if value == "" {
		value = fmt.Sprintf("user-%d", time.Now().UnixMilli())
	}
	value = strings.ReplaceAll(value, "*", "x")
	value = iflowCleanerPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		value = fmt.Sprintf("user-%d", time.Now().UnixMilli())
	}
	return fmt.Sprintf("iflow-%s-%d.json", value, time.Now().Unix())
}

func (m *IFlowSubscriptionManager) setSessionError(state, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[state]
	if !ok {
		return
	}
	session.Status = "error"
	session.ErrorMessage = strings.TrimSpace(message)
	session.UpdatedAt = time.Now()
}

func (m *IFlowSubscriptionManager) completeSession(state string, credential *IFlowSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[state]
	if !ok {
		return
	}
	session.Status = "ok"
	session.ErrorMessage = ""
	session.Credential = credential
	session.UpdatedAt = time.Now()
}

func (m *IFlowSubscriptionManager) cleanupSessionsLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	for state, session := range m.sessions {
		if session == nil {
			delete(m.sessions, state)
			continue
		}
		if session.Status == "pending" && now.After(session.ExpiresAt) {
			session.Status = "error"
			session.ErrorMessage = "授权会话已过期，请重新发起"
			session.UpdatedAt = now
		}
		if session.Status != "pending" && now.Sub(session.UpdatedAt) > iflowSessionRetention {
			delete(m.sessions, state)
		}
	}
}

func buildIFlowAuthorizeURL(clientID, redirectURI, state string) (string, error) {
	authURL := strings.TrimSpace(os.Getenv("IFLOW_OAUTH_AUTH_URL"))
	if authURL == "" {
		authURL = defaultIFlowOAuthAuthURL
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}

	query := parsed.Query()
	query.Set("loginMethod", "phone")
	query.Set("type", "phone")
	query.Set("redirect", redirectURI)
	query.Set("state", state)
	query.Set("client_id", clientID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func getIFlowOAuthClientID() string {
	clientID := strings.TrimSpace(os.Getenv("IFLOW_OAUTH_CLIENT_ID"))
	if clientID == "" {
		clientID = defaultIFlowOAuthClientID
	}
	return clientID
}

func getIFlowOAuthClientSecret() string {
	secret := strings.TrimSpace(os.Getenv("IFLOW_CLIENT_SECRET"))
	if secret == "" {
		secret = defaultIFlowOAuthSecret
	}
	return secret
}

func (m *IFlowSubscriptionManager) exchangeCodeForTokens(ctx context.Context, code, redirectURI string) (*iflowOAuthTokenResponse, error) {
	code = strings.TrimSpace(code)
	redirectURI = strings.TrimSpace(redirectURI)
	if code == "" {
		return nil, errors.New("iflow oauth code 为空")
	}
	if redirectURI == "" {
		return nil, errors.New("iflow oauth redirect_uri 为空")
	}

	tokenURL := strings.TrimSpace(os.Getenv("IFLOW_OAUTH_TOKEN_URL"))
	if tokenURL == "" {
		tokenURL = defaultIFlowOAuthTokenURL
	}

	clientID := getIFlowOAuthClientID()
	clientSecret := getIFlowOAuthClientSecret()
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	basic := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+basic)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iflow token 请求失败(%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp iflowOAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, errors.New("iflow token 响应缺少 access_token")
	}
	return &tokenResp, nil
}

func (m *IFlowSubscriptionManager) exchangeRefreshTokenForTokens(ctx context.Context, refreshToken string) (*iflowOAuthTokenResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errors.New("iflow oauth refresh_token 为空")
	}

	tokenURL := strings.TrimSpace(os.Getenv("IFLOW_OAUTH_TOKEN_URL"))
	if tokenURL == "" {
		tokenURL = defaultIFlowOAuthTokenURL
	}

	clientID := getIFlowOAuthClientID()
	clientSecret := getIFlowOAuthClientSecret()
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	basic := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+basic)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iflow token 请求失败(%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp iflowOAuthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, errors.New("iflow token 响应缺少 access_token")
	}
	return &tokenResp, nil
}

func (m *IFlowSubscriptionManager) fetchOAuthUserInfo(ctx context.Context, accessToken string) (*iflowOAuthUserInfoResponse, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return nil, errors.New("iflow access_token 为空")
	}

	userInfoURL := strings.TrimSpace(os.Getenv("IFLOW_OAUTH_USERINFO_URL"))
	if userInfoURL == "" {
		userInfoURL = defaultIFlowOAuthUserInfo
	}
	endpoint := fmt.Sprintf("%s?accessToken=%s", userInfoURL, url.QueryEscape(accessToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iflow userinfo 请求失败(%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var userInfo iflowOAuthUserInfoResponse
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return nil, err
	}
	if !userInfo.Success {
		return nil, errors.New("iflow userinfo 接口返回失败")
	}
	return &userInfo, nil
}

func BuildIFlowOAuthRedirectURI(r *http.Request) string {
	override := strings.TrimSpace(os.Getenv("IFLOW_OAUTH_REDIRECT_URL"))
	if override != "" {
		return override
	}

	if r == nil {
		port := strings.TrimSpace(os.Getenv("LLMIO_SERVER_PORT"))
		if port == "" {
			port = "7070"
		}
		return fmt.Sprintf("http://localhost:%s%s", port, defaultIFlowOAuthCallback)
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(proto)
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost:7070"
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, defaultIFlowOAuthCallback)
}
