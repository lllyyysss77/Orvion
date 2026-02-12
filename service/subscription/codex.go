package subscription

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultCodexOAuthAuthURL          = "https://auth.openai.com/oauth/authorize"
	defaultCodexOAuthTokenURL         = "https://auth.openai.com/oauth/token"
	defaultCodexOAuthClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultCodexOAuthScope            = "openid profile email offline_access"
	defaultCodexOAuthFixedRedirectURL = "http://localhost:1455/auth/callback"
	defaultCodexOAuthForwarderAddr    = "localhost:1455"
	defaultCodexSubscriptionDir       = "data/codex-auths"
	defaultCodexBackendBaseURL        = "https://chatgpt.com/backend-api/codex"
	defaultCodexUsageURL              = "https://chatgpt.com/backend-api/wham/usage"
	defaultCodexQuotaProbeModel       = "gpt-5-codex"
	codexSessionTTL                   = 10 * time.Minute
	codexSessionRetention             = 30 * time.Minute
	codexCallbackPollInterval         = 1 * time.Second
	codexTokenRefreshInterval         = 30 * time.Minute
	codexTokenRefreshLead             = 5 * 24 * time.Hour
	codexTokenExpirySkew              = 30 * time.Second
	codexStateLength                  = 40
	codexVerifierLength               = 64
	codexTempCallbackPrefix           = ".oauth-codex-"
	codexTempCallbackSuffix           = ".oauth"
	defaultCodexOAuthCallbackPath     = "/codex/callback"
	codexFiveHourWindowSeconds        = int64(18000)
	codexWeeklyWindowSeconds          = int64(604800)
	codexClientVersion                = "0.98.0"
	codexClientUserAgent              = "codex_cli_rs/0.98.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
	codexClientOriginator             = "codex_cli_rs"
)

var (
	ErrCodexOAuthNotConfigured   = errors.New("未配置 CODEX_OAUTH_CLIENT_ID")
	ErrCodexInvalidState         = errors.New("非法 state 参数")
	ErrCodexSessionNotFound      = errors.New("授权会话不存在")
	ErrCodexSessionNotPending    = errors.New("授权会话已结束")
	ErrCodexSubscriptionNotFound = errors.New("订阅凭据不存在")

	codexStatePattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	codexCredentialIDPattern = regexp.MustCompile(`^codex-[A-Za-z0-9._@-]+\.json$`)
	codexCleanerPattern      = regexp.MustCompile(`[^a-z0-9._-]+`)

	// 参考 CLIProxyAPIPlus: Codex 可用模型由本地注册表静态维护，不直接调用上游模型列表接口。
	codexOpenAIModels = []CodexModel{
		{ID: "gpt-5", Object: "model", Created: 1754524800, OwnedBy: "openai"},
		{ID: "gpt-5-codex", Object: "model", Created: 1757894400, OwnedBy: "openai"},
		{ID: "gpt-5-codex-mini", Object: "model", Created: 1762473600, OwnedBy: "openai"},
		{ID: "gpt-5.1", Object: "model", Created: 1762905600, OwnedBy: "openai"},
		{ID: "gpt-5.1-codex", Object: "model", Created: 1762905600, OwnedBy: "openai"},
		{ID: "gpt-5.1-codex-mini", Object: "model", Created: 1762905600, OwnedBy: "openai"},
		{ID: "gpt-5.1-codex-max", Object: "model", Created: 1763424000, OwnedBy: "openai"},
		{ID: "gpt-5.2", Object: "model", Created: 1765440000, OwnedBy: "openai"},
		{ID: "gpt-5.2-codex", Object: "model", Created: 1765440000, OwnedBy: "openai"},
		{ID: "gpt-5.3-codex", Object: "model", Created: 1770307200, OwnedBy: "openai"},
	}
)

type CodexOAuthStartResult struct {
	State     string    `json:"state"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CodexOAuthStatusResult struct {
	State      string             `json:"state"`
	Status     string             `json:"status"` // wait / ok / error
	Message    string             `json:"message,omitempty"`
	Credential *CodexSubscription `json:"credential,omitempty"`
}

type CodexSubscription struct {
	ID                      string    `json:"id"`
	FileName                string    `json:"file_name"`
	Email                   string    `json:"email,omitempty"`
	PlanType                string    `json:"plan_type,omitempty"`
	AccountID               string    `json:"account_id,omitempty"`
	SubscriptionActiveStart string    `json:"subscription_active_start,omitempty"`
	SubscriptionActiveUntil string    `json:"subscription_active_until,omitempty"`
	LastRefresh             string    `json:"last_refresh,omitempty"`
	Expired                 string    `json:"expired,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type CodexModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type CodexRequestCredential struct {
	SubscriptionID string `json:"subscription_id"`
	AccessToken    string `json:"access_token"`
	AccountID      string `json:"account_id,omitempty"`
	PlanType       string `json:"plan_type,omitempty"`
}

type CodexSubscriptionQuota struct {
	SubscriptionID    string             `json:"subscription_id"`
	Model             string             `json:"model"`
	ProbeURL          string             `json:"probe_url"`
	HTTPStatus        int                `json:"http_status"`
	PlanType          string             `json:"plan_type,omitempty"`
	Windows           []CodexQuotaWindow `json:"windows,omitempty"`
	RequestLimit      *int64             `json:"request_limit,omitempty"`
	RequestRemaining  *int64             `json:"request_remaining,omitempty"`
	RequestReset      string             `json:"request_reset,omitempty"`
	RequestResetAt    string             `json:"request_reset_at,omitempty"`
	TokenLimit        *int64             `json:"token_limit,omitempty"`
	TokenRemaining    *int64             `json:"token_remaining,omitempty"`
	TokenReset        string             `json:"token_reset,omitempty"`
	TokenResetAt      string             `json:"token_reset_at,omitempty"`
	ResetTime         string             `json:"reset_time,omitempty"`
	ResetAt           string             `json:"reset_at,omitempty"`
	Message           string             `json:"message,omitempty"`
	Source            string             `json:"source,omitempty"`
	RawRateLimitHints []any              `json:"raw_rate_limit_hints,omitempty"`
}

type CodexQuotaWindow struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	UsedPercent        *float64 `json:"used_percent,omitempty"`
	RemainingPercent   *float64 `json:"remaining_percent,omitempty"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds,omitempty"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds,omitempty"`
	ResetAt            string   `json:"reset_at,omitempty"`
	ResetLabel         string   `json:"reset_label,omitempty"`
}

type codexUsagePayload struct {
	PlanType            string              `json:"plan_type"`
	RateLimit           *codexRateLimitInfo `json:"rate_limit"`
	CodeReviewRateLimit *codexRateLimitInfo `json:"code_review_rate_limit"`
}

type codexRateLimitInfo struct {
	Allowed         *bool             `json:"allowed"`
	LimitReached    *bool             `json:"limit_reached"`
	PrimaryWindow   *codexUsageWindow `json:"primary_window"`
	SecondaryWindow *codexUsageWindow `json:"secondary_window"`
}

type codexUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

type codexOAuthSession struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	Status       string // pending / ok / error
	ErrorMessage string
	Credential   *CodexSubscription
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

type codexCallbackPayload struct {
	Code             string `json:"code"`
	State            string `json:"state"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type codexIDTokenClaims struct {
	Email                   string
	AccountID               string
	PlanType                string
	SubscriptionActiveStart string
	SubscriptionActiveUntil string
}

type codexCredentialRecord struct {
	IDToken                 string `json:"id_token"`
	AccessToken             string `json:"access_token"`
	RefreshToken            string `json:"refresh_token,omitempty"`
	AccountID               string `json:"account_id,omitempty"`
	PlanType                string `json:"plan_type,omitempty"`
	SubscriptionActiveStart string `json:"subscription_active_start,omitempty"`
	SubscriptionActiveUntil string `json:"subscription_active_until,omitempty"`
	LastRefresh             string `json:"last_refresh"`
	Email                   string `json:"email,omitempty"`
	Type                    string `json:"type"`
	Expired                 string `json:"expired,omitempty"`
}

type CodexSubscriptionManager struct {
	mu           sync.RWMutex
	credentialMu sync.Mutex
	baseDir      string
	authDir      string
	sessions     map[string]*codexOAuthSession
	client       *http.Client
}

var (
	codexSubscriptionManager     *CodexSubscriptionManager
	codexSubscriptionManagerOnce sync.Once
	codexOAuthWorkerOnce         sync.Once
	codexOAuthForwarderOnce      sync.Once
)

func GetCodexSubscriptionManager() *CodexSubscriptionManager {
	codexSubscriptionManagerOnce.Do(func() {
		baseDir := strings.TrimSpace(os.Getenv("CODEX_SUBSCRIPTION_DIR"))
		if baseDir == "" {
			baseDir = defaultCodexSubscriptionDir
		}
		manager, err := NewCodexSubscriptionManager(baseDir)
		if err != nil {
			slog.Error("初始化 Codex 订阅目录失败，回退到默认目录", "error", err)
			manager, err = NewCodexSubscriptionManager(defaultCodexSubscriptionDir)
			if err != nil {
				slog.Error("使用默认目录初始化 Codex 订阅目录失败，回退临时目录", "error", err)
				manager, err = NewCodexSubscriptionManager(filepath.Join(os.TempDir(), "llmio-codex-auths"))
				if err != nil {
					slog.Error("初始化临时 Codex 订阅目录失败，使用内存目录模式", "error", err)
					manager = &CodexSubscriptionManager{
						baseDir:  "",
						authDir:  "",
						sessions: make(map[string]*codexOAuthSession),
						client:   &http.Client{Timeout: 15 * time.Second},
					}
				}
			}
		}
		codexSubscriptionManager = manager
	})
	return codexSubscriptionManager
}

func NewCodexSubscriptionManager(baseDir string) (*CodexSubscriptionManager, error) {
	cleanBaseDir := strings.TrimSpace(baseDir)
	if cleanBaseDir == "" {
		cleanBaseDir = defaultCodexSubscriptionDir
	}
	authDir := filepath.Join(cleanBaseDir, "auths")
	if err := os.MkdirAll(cleanBaseDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return nil, err
	}

	return &CodexSubscriptionManager{
		baseDir:  cleanBaseDir,
		authDir:  authDir,
		sessions: make(map[string]*codexOAuthSession),
		client:   &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func StartCodexOAuthWorker(ctx context.Context) {
	codexOAuthWorkerOnce.Do(func() {
		go GetCodexSubscriptionManager().worker(ctx)
	})
}

func StartCodexOAuthCallbackForwarder(ctx context.Context) {
	codexOAuthForwarderOnce.Do(func() {
		if !shouldStartCodexCallbackForwarder() {
			return
		}

		listenAddr := strings.TrimSpace(os.Getenv("CODEX_OAUTH_FORWARDER_ADDR"))
		if listenAddr == "" {
			listenAddr = defaultCodexOAuthForwarderAddr
		}

		targetURL := strings.TrimSpace(os.Getenv("CODEX_OAUTH_INTERNAL_CALLBACK_URL"))
		if targetURL == "" {
			port := strings.TrimSpace(os.Getenv("LLMIO_SERVER_PORT"))
			if port == "" {
				port = "7070"
			}
			targetURL = fmt.Sprintf("http://localhost:%s%s", port, defaultCodexOAuthCallbackPath)
		}

		targetURL = strings.TrimRight(targetURL, "?")
		mux := http.NewServeMux()
		mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
			redirectURL := targetURL
			if r.URL.RawQuery != "" {
				redirectURL = redirectURL + "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, redirectURL, http.StatusFound)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("codex oauth callback forwarder"))
		})

		server := &http.Server{
			Addr:              listenAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}

		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()

		go func() {
			slog.Info("Codex OAuth 回调转发器已启动", "listen", listenAddr, "target", targetURL)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Warn("Codex OAuth 回调转发器启动失败", "error", err, "listen", listenAddr)
			}
		}()
	})
}

func (m *CodexSubscriptionManager) worker(ctx context.Context) {
	callbackTicker := time.NewTicker(codexCallbackPollInterval)
	refreshTicker := time.NewTicker(codexTokenRefreshInterval)
	defer callbackTicker.Stop()
	defer refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-callbackTicker.C:
			if err := m.consumeCallbackFiles(ctx); err != nil {
				slog.Warn("消费 Codex OAuth 回调文件失败", "error", err)
			}
			m.cleanupSessions()
		case <-refreshTicker.C:
			m.refreshSubscriptionsIfNeeded(ctx)
		}
	}
}

func (m *CodexSubscriptionManager) StartOAuthSession(redirectURI string) (*CodexOAuthStartResult, error) {
	clientID := getCodexOAuthClientID()
	if clientID == "" {
		return nil, ErrCodexOAuthNotConfigured
	}

	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		return nil, errors.New("redirect_uri 不能为空")
	}

	state, err := generateRandomString(codexStateLength)
	if err != nil {
		return nil, fmt.Errorf("生成 state 失败: %w", err)
	}
	verifier, err := generateRandomString(codexVerifierLength)
	if err != nil {
		return nil, fmt.Errorf("生成 PKCE verifier 失败: %w", err)
	}
	challenge := buildPKCEChallenge(verifier)

	authURL, err := buildCodexAuthorizeURL(clientID, redirectURI, state, challenge)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	session := &codexOAuthSession{
		State:        state,
		CodeVerifier: verifier,
		RedirectURI:  redirectURI,
		Status:       "pending",
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(codexSessionTTL),
	}

	m.mu.Lock()
	m.sessions[state] = session
	m.mu.Unlock()

	return &CodexOAuthStartResult{
		State:     state,
		AuthURL:   authURL,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

func (m *CodexSubscriptionManager) PersistCallback(code, state, authErr, authErrDesc string) error {
	state = strings.TrimSpace(state)
	if !isValidCodexState(state) {
		return ErrCodexInvalidState
	}
	if strings.TrimSpace(m.baseDir) == "" {
		return errors.New("Codex 回调目录不可用")
	}

	m.mu.RLock()
	session, ok := m.sessions[state]
	m.mu.RUnlock()
	if !ok {
		return ErrCodexSessionNotFound
	}
	if session.Status != "pending" {
		return ErrCodexSessionNotPending
	}

	payload := codexCallbackPayload{
		Code:             strings.TrimSpace(code),
		State:            state,
		Error:            strings.TrimSpace(authErr),
		ErrorDescription: strings.TrimSpace(authErrDesc),
	}

	callbackFile := filepath.Join(m.baseDir, codexTempCallbackPrefix+state+codexTempCallbackSuffix)
	return writeJSON0600(callbackFile, payload)
}

func (m *CodexSubscriptionManager) GetOAuthStatus(state string) (*CodexOAuthStatusResult, error) {
	state = strings.TrimSpace(state)
	if !isValidCodexState(state) {
		return nil, ErrCodexInvalidState
	}

	m.mu.RLock()
	session, ok := m.sessions[state]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrCodexSessionNotFound
	}

	status := "wait"
	switch session.Status {
	case "ok":
		status = "ok"
	case "error":
		status = "error"
	}

	result := &CodexOAuthStatusResult{
		State:      state,
		Status:     status,
		Message:    session.ErrorMessage,
		Credential: session.Credential,
	}
	return result, nil
}

func (m *CodexSubscriptionManager) ListSubscriptions() ([]CodexSubscription, error) {
	if strings.TrimSpace(m.authDir) == "" {
		return []CodexSubscription{}, nil
	}
	files, err := collectCredentialFiles(m.authDir, codexCredentialIDPattern)
	if err != nil {
		return nil, err
	}

	subs := make([]CodexSubscription, 0, len(files))
	for _, file := range files {
		raw, readErr := os.ReadFile(file.Path)
		if readErr != nil {
			slog.Warn("读取 Codex 凭据文件失败", "file", file.RelativePath, "error", readErr)
			continue
		}

		var record codexCredentialRecord
		if unmarshalErr := json.Unmarshal(raw, &record); unmarshalErr != nil {
			slog.Warn("解析 Codex 凭据文件失败", "file", file.RelativePath, "error", unmarshalErr)
			continue
		}
		// 兼容历史凭据：若文件内未写入套餐/账号字段，则尝试从 id_token 回填。
		if strings.TrimSpace(record.IDToken) != "" {
			if claims, parseErr := parseCodexIDTokenClaims(record.IDToken); parseErr == nil {
				if strings.TrimSpace(record.PlanType) == "" {
					record.PlanType = strings.TrimSpace(claims.PlanType)
				}
				if strings.TrimSpace(record.AccountID) == "" {
					record.AccountID = strings.TrimSpace(claims.AccountID)
				}
				if strings.TrimSpace(record.SubscriptionActiveStart) == "" {
					record.SubscriptionActiveStart = strings.TrimSpace(claims.SubscriptionActiveStart)
				}
				if strings.TrimSpace(record.SubscriptionActiveUntil) == "" {
					record.SubscriptionActiveUntil = strings.TrimSpace(claims.SubscriptionActiveUntil)
				}
			}
		}

		sub := CodexSubscription{
			ID:                      file.Name,
			FileName:                file.Name,
			Email:                   record.Email,
			PlanType:                record.PlanType,
			AccountID:               record.AccountID,
			SubscriptionActiveStart: record.SubscriptionActiveStart,
			SubscriptionActiveUntil: record.SubscriptionActiveUntil,
			LastRefresh:             record.LastRefresh,
			Expired:                 record.Expired,
			CreatedAt:               file.ModTime,
			UpdatedAt:               file.ModTime,
		}
		subs = append(subs, sub)
	}

	sort.Slice(subs, func(i, j int) bool {
		if subs[i].UpdatedAt.Equal(subs[j].UpdatedAt) {
			return subs[i].ID < subs[j].ID
		}
		return subs[i].UpdatedAt.After(subs[j].UpdatedAt)
	})

	return subs, nil
}

func (m *CodexSubscriptionManager) DeleteSubscription(id string) error {
	if strings.TrimSpace(m.authDir) == "" {
		return ErrCodexSubscriptionNotFound
	}
	id = strings.TrimSpace(id)
	if !codexCredentialIDPattern.MatchString(id) {
		return ErrCodexSubscriptionNotFound
	}
	if strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return ErrCodexSubscriptionNotFound
	}

	fullPath, err := resolveCredentialPath(m.authDir, id, codexCredentialIDPattern)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrCodexSubscriptionNotFound
		}
		return err
	}

	if err := os.Remove(fullPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrCodexSubscriptionNotFound
		}
		return err
	}

	return nil
}

func (m *CodexSubscriptionManager) ListAvailableModels(ctx context.Context, subscriptionID string) ([]CodexModel, error) {
	_, err := m.readCredentialRecord(subscriptionID)
	if err != nil {
		return nil, err
	}
	_ = ctx

	models := make([]CodexModel, 0, len(codexOpenAIModels))
	models = append(models, codexOpenAIModels...)
	return models, nil
}

func (m *CodexSubscriptionManager) GetAccessToken(ctx context.Context, subscriptionID string) (string, error) {
	record, err := m.readCredentialForUse(ctx, subscriptionID)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(record.AccessToken)
	if token == "" {
		return "", errors.New("订阅凭据缺少 access_token")
	}
	return token, nil
}

func (m *CodexSubscriptionManager) ResolveRequestCredential(ctx context.Context, preferredSubscriptionID string) (*CodexRequestCredential, error) {
	subscriptionID := strings.TrimSpace(preferredSubscriptionID)
	if subscriptionID == "" {
		subscriptions, err := m.ListSubscriptions()
		if err != nil {
			return nil, err
		}
		if len(subscriptions) == 0 {
			return nil, ErrCodexSubscriptionNotFound
		}
		subscriptionID = subscriptions[0].ID
	}

	record, err := m.readCredentialForUse(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(record.AccessToken)
	if token == "" {
		return nil, errors.New("订阅凭据缺少 access_token")
	}

	accountID := strings.TrimSpace(record.AccountID)
	planType := strings.TrimSpace(record.PlanType)
	if strings.TrimSpace(record.IDToken) != "" {
		if claims, parseErr := parseCodexIDTokenClaims(record.IDToken); parseErr == nil {
			if accountID == "" {
				accountID = strings.TrimSpace(claims.AccountID)
			}
			if planType == "" {
				planType = strings.TrimSpace(claims.PlanType)
			}
		}
	}

	return &CodexRequestCredential{
		SubscriptionID: subscriptionID,
		AccessToken:    token,
		AccountID:      accountID,
		PlanType:       planType,
	}, nil
}

func (m *CodexSubscriptionManager) GetSubscriptionQuota(ctx context.Context, subscriptionID string) (*CodexSubscriptionQuota, error) {
	record, err := m.readCredentialForUse(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(record.AccessToken)
	if token == "" {
		return nil, errors.New("订阅凭据缺少 access_token")
	}

	accountID := strings.TrimSpace(record.AccountID)
	if accountID == "" && strings.TrimSpace(record.IDToken) != "" {
		if claims, parseErr := parseCodexIDTokenClaims(record.IDToken); parseErr == nil {
			accountID = strings.TrimSpace(claims.AccountID)
		}
	}
	if accountID == "" {
		return nil, errors.New("订阅凭据缺少 account_id")
	}

	usageURL := strings.TrimSpace(os.Getenv("CODEX_USAGE_URL"))
	if usageURL == "" {
		usageURL = defaultCodexUsageURL
	}
	usageURL = strings.TrimRight(usageURL, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal")
	req.Header.Set("Chatgpt-Account-Id", accountID)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	quota := &CodexSubscriptionQuota{
		SubscriptionID: subscriptionID,
		Model:          defaultCodexQuotaProbeModel,
		ProbeURL:       usageURL,
		HTTPStatus:     resp.StatusCode,
	}

	collectQuotaFromUsagePayload(quota, respBody)
	collectQuotaFromHeaders(quota, resp.Header)
	if resp.StatusCode >= http.StatusBadRequest {
		collectQuotaFromBody(quota, respBody)
	}
	normalizeCodexQuota(quota)
	return quota, nil
}

func (m *CodexSubscriptionManager) consumeCallbackFiles(ctx context.Context) error {
	if strings.TrimSpace(m.baseDir) == "" {
		return nil
	}
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, codexTempCallbackPrefix) || !strings.HasSuffix(name, codexTempCallbackSuffix) {
			continue
		}

		fullPath := filepath.Join(m.baseDir, name)
		raw, readErr := os.ReadFile(fullPath)
		_ = os.Remove(fullPath)
		if readErr != nil {
			slog.Warn("读取临时 OAuth 回调文件失败", "file", name, "error", readErr)
			continue
		}

		var payload codexCallbackPayload
		if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr != nil {
			slog.Warn("解析临时 OAuth 回调文件失败", "file", name, "error", unmarshalErr)
			continue
		}

		m.handleCallback(ctx, payload)
	}

	return nil
}

func (m *CodexSubscriptionManager) readCredentialRecord(id string) (codexCredentialRecord, error) {
	record, _, err := m.readCredentialRecordWithPath(id)
	if err != nil {
		return codexCredentialRecord{}, err
	}
	return record, nil
}

func (m *CodexSubscriptionManager) readCredentialRecordWithPath(id string) (codexCredentialRecord, string, error) {
	if strings.TrimSpace(m.authDir) == "" {
		return codexCredentialRecord{}, "", ErrCodexSubscriptionNotFound
	}

	id = strings.TrimSpace(id)
	if !codexCredentialIDPattern.MatchString(id) {
		return codexCredentialRecord{}, "", ErrCodexSubscriptionNotFound
	}
	if strings.Contains(id, "/") || strings.Contains(id, "\\") {
		return codexCredentialRecord{}, "", ErrCodexSubscriptionNotFound
	}

	fullPath, err := resolveCredentialPath(m.authDir, id, codexCredentialIDPattern)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return codexCredentialRecord{}, "", ErrCodexSubscriptionNotFound
		}
		return codexCredentialRecord{}, "", err
	}

	raw, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return codexCredentialRecord{}, "", ErrCodexSubscriptionNotFound
		}
		return codexCredentialRecord{}, "", err
	}

	var record codexCredentialRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return codexCredentialRecord{}, "", err
	}

	return record, fullPath, nil
}

func (m *CodexSubscriptionManager) refreshSubscriptionsIfNeeded(ctx context.Context) {
	subs, err := m.ListSubscriptions()
	if err != nil {
		slog.Warn("扫描 Codex 订阅失败，跳过定时刷新", "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}

	for _, sub := range subs {
		refreshCtx := ctx
		cancel := func() {}
		if refreshCtx == nil {
			refreshCtx = context.Background()
		}
		refreshCtx, cancel = context.WithTimeout(refreshCtx, 20*time.Second)
		_, refreshErr := m.readCredentialForUse(refreshCtx, sub.ID)
		cancel()
		if refreshErr != nil {
			slog.Warn("定时刷新 Codex 凭据失败", "subscription", sub.ID, "error", refreshErr)
		}
	}
}

func (m *CodexSubscriptionManager) readCredentialForUse(ctx context.Context, subscriptionID string) (codexCredentialRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	now := time.Now().UTC()
	record, fullPath, err := m.readCredentialRecordWithPath(subscriptionID)
	if err != nil {
		return codexCredentialRecord{}, err
	}

	if !m.shouldRefreshCredential(record, now) {
		if isCredentialRecordExpired(record, now) {
			return codexCredentialRecord{}, errors.New("订阅凭据已过期，请重新授权")
		}
		return record, nil
	}

	if strings.TrimSpace(record.RefreshToken) == "" {
		if isCredentialRecordExpired(record, now) {
			return codexCredentialRecord{}, errors.New("订阅凭据已过期且缺少 refresh_token，请重新授权")
		}
		return record, nil
	}

	m.credentialMu.Lock()
	defer m.credentialMu.Unlock()

	// 锁内重读，避免并发请求重复刷新同一文件。
	record, fullPath, err = m.readCredentialRecordWithPath(subscriptionID)
	if err != nil {
		return codexCredentialRecord{}, err
	}
	now = time.Now().UTC()
	if !m.shouldRefreshCredential(record, now) {
		if isCredentialRecordExpired(record, now) {
			return codexCredentialRecord{}, errors.New("订阅凭据已过期，请重新授权")
		}
		return record, nil
	}

	updated, refreshErr := m.refreshCredentialRecord(ctx, record)
	if refreshErr != nil {
		// 未到真正过期时，保留旧 token 继续使用，避免短暂网络抖动影响业务请求。
		if !isCredentialRecordExpired(record, now) {
			slog.Warn("刷新 Codex token 失败，继续使用当前 token", "subscription", subscriptionID, "error", refreshErr)
			return record, nil
		}
		return codexCredentialRecord{}, refreshErr
	}

	if err := writeJSON0600(fullPath, updated); err != nil {
		return codexCredentialRecord{}, fmt.Errorf("写入刷新后的凭据失败: %w", err)
	}

	return updated, nil
}

func (m *CodexSubscriptionManager) shouldRefreshCredential(record codexCredentialRecord, now time.Time) bool {
	if isCredentialRecordExpired(record, now) {
		return true
	}

	expAt := parseRFC3339OrZero(record.Expired)
	if !expAt.IsZero() {
		return expAt.Sub(now) <= codexTokenRefreshLead
	}

	lastRefreshAt := parseRFC3339OrZero(record.LastRefresh)
	if !lastRefreshAt.IsZero() {
		return now.Sub(lastRefreshAt) >= codexTokenRefreshLead
	}

	return false
}

func isCredentialRecordExpired(record codexCredentialRecord, now time.Time) bool {
	expAt := parseRFC3339OrZero(record.Expired)
	if expAt.IsZero() {
		return false
	}
	return !now.Before(expAt.Add(-codexTokenExpirySkew))
}

func (m *CodexSubscriptionManager) refreshCredentialRecord(ctx context.Context, record codexCredentialRecord) (codexCredentialRecord, error) {
	refreshToken := strings.TrimSpace(record.RefreshToken)
	if refreshToken == "" {
		return codexCredentialRecord{}, errors.New("订阅凭据缺少 refresh_token")
	}

	tokenResp, err := m.refreshAccessToken(ctx, refreshToken)
	if err != nil {
		return codexCredentialRecord{}, err
	}

	now := time.Now().UTC()
	updated := record
	updated.AccessToken = strings.TrimSpace(tokenResp.AccessToken)
	if updated.AccessToken == "" {
		return codexCredentialRecord{}, errors.New("刷新 token 响应缺少 access_token")
	}
	if refreshedToken := strings.TrimSpace(tokenResp.RefreshToken); refreshedToken != "" {
		updated.RefreshToken = refreshedToken
	}
	if refreshedIDToken := strings.TrimSpace(tokenResp.IDToken); refreshedIDToken != "" {
		updated.IDToken = refreshedIDToken
	}

	if strings.TrimSpace(updated.IDToken) != "" {
		if claims, parseErr := parseCodexIDTokenClaims(updated.IDToken); parseErr == nil {
			if strings.TrimSpace(claims.Email) != "" {
				updated.Email = claims.Email
			}
			if strings.TrimSpace(claims.AccountID) != "" {
				updated.AccountID = claims.AccountID
			}
			if strings.TrimSpace(claims.PlanType) != "" {
				updated.PlanType = claims.PlanType
			}
			if strings.TrimSpace(claims.SubscriptionActiveStart) != "" {
				updated.SubscriptionActiveStart = claims.SubscriptionActiveStart
			}
			if strings.TrimSpace(claims.SubscriptionActiveUntil) != "" {
				updated.SubscriptionActiveUntil = claims.SubscriptionActiveUntil
			}
		} else {
			slog.Warn("解析刷新后的 id_token 失败，保留原有账户信息", "error", parseErr)
		}
	}

	updated.LastRefresh = now.Format(time.RFC3339)
	if tokenResp.ExpiresIn > 0 {
		updated.Expired = now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if strings.TrimSpace(updated.Type) == "" {
		updated.Type = "codex"
	}

	return updated, nil
}

func (m *CodexSubscriptionManager) refreshAccessToken(ctx context.Context, refreshToken string) (*codexTokenResponse, error) {
	clientID := getCodexOAuthClientID()
	if clientID == "" {
		return nil, ErrCodexOAuthNotConfigured
	}

	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(refreshToken))
	values.Set("scope", "openid profile email")

	tokenURL := strings.TrimSpace(os.Getenv("CODEX_OAUTH_TOKEN_URL"))
	if tokenURL == "" {
		tokenURL = defaultCodexOAuthTokenURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("刷新 token 失败，HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp codexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析刷新 token 响应失败: %w", err)
	}
	return &tokenResp, nil
}

func (m *CodexSubscriptionManager) handleCallback(ctx context.Context, payload codexCallbackPayload) {
	state := strings.TrimSpace(payload.State)
	if !isValidCodexState(state) {
		slog.Warn("忽略非法 state 的 OAuth 回调", "state", state)
		return
	}

	m.mu.RLock()
	session, ok := m.sessions[state]
	if !ok {
		m.mu.RUnlock()
		return
	}
	if session.Status != "pending" {
		m.mu.RUnlock()
		return
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		m.mu.RUnlock()
		m.setSessionError(state, "授权已超时，请重新发起")
		return
	}

	codeVerifier := session.CodeVerifier
	redirectURI := session.RedirectURI
	m.mu.RUnlock()

	if payload.Error != "" {
		msg := payload.Error
		if payload.ErrorDescription != "" {
			msg = msg + ": " + payload.ErrorDescription
		}
		m.setSessionError(state, msg)
		return
	}

	if strings.TrimSpace(payload.Code) == "" {
		m.setSessionError(state, "回调缺少 code")
		return
	}

	tokenResp, err := m.exchangeCodeForTokens(ctx, payload.Code, codeVerifier, redirectURI)
	if err != nil {
		m.setSessionError(state, "code 换 token 失败: "+err.Error())
		return
	}

	claims, err := parseCodexIDTokenClaims(tokenResp.IDToken)
	if err != nil {
		slog.Warn("解析 id_token 失败，将继续保存基础凭据", "error", err)
	}

	subscription, err := m.saveCredential(tokenResp, claims)
	if err != nil {
		m.setSessionError(state, "保存凭据失败: "+err.Error())
		return
	}

	m.completeSession(state, subscription)
}

func (m *CodexSubscriptionManager) exchangeCodeForTokens(ctx context.Context, code, codeVerifier, redirectURI string) (*codexTokenResponse, error) {
	clientID := getCodexOAuthClientID()
	if clientID == "" {
		return nil, ErrCodexOAuthNotConfigured
	}

	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("client_id", clientID)
	values.Set("code", strings.TrimSpace(code))
	values.Set("redirect_uri", strings.TrimSpace(redirectURI))
	values.Set("code_verifier", strings.TrimSpace(codeVerifier))

	tokenURL := strings.TrimSpace(os.Getenv("CODEX_OAUTH_TOKEN_URL"))
	if tokenURL == "" {
		tokenURL = defaultCodexOAuthTokenURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp codexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" || strings.TrimSpace(tokenResp.IDToken) == "" {
		return nil, errors.New("token 响应缺少 access_token 或 id_token")
	}
	return &tokenResp, nil
}

func (m *CodexSubscriptionManager) saveCredential(tokens *codexTokenResponse, claims codexIDTokenClaims) (*CodexSubscription, error) {
	if strings.TrimSpace(m.authDir) == "" {
		return nil, errors.New("Codex 凭据目录不可用")
	}
	now := time.Now().UTC()
	filename := buildCredentialFileName(claims)
	fullPath, err := m.nextCredentialPath(filename)
	if err != nil {
		return nil, err
	}

	record := codexCredentialRecord{
		IDToken:                 tokens.IDToken,
		AccessToken:             tokens.AccessToken,
		RefreshToken:            tokens.RefreshToken,
		AccountID:               claims.AccountID,
		PlanType:                claims.PlanType,
		SubscriptionActiveStart: claims.SubscriptionActiveStart,
		SubscriptionActiveUntil: claims.SubscriptionActiveUntil,
		LastRefresh:             now.Format(time.RFC3339),
		Email:                   claims.Email,
		Type:                    "codex",
	}
	if tokens.ExpiresIn > 0 {
		record.Expired = now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
	}

	if err := writeJSON0600(fullPath, record); err != nil {
		return nil, err
	}

	baseName := filepath.Base(fullPath)
	return &CodexSubscription{
		ID:                      baseName,
		FileName:                baseName,
		Email:                   claims.Email,
		PlanType:                claims.PlanType,
		AccountID:               claims.AccountID,
		SubscriptionActiveStart: claims.SubscriptionActiveStart,
		SubscriptionActiveUntil: claims.SubscriptionActiveUntil,
		LastRefresh:             record.LastRefresh,
		Expired:                 record.Expired,
		CreatedAt:               now,
		UpdatedAt:               now,
	}, nil
}

func (m *CodexSubscriptionManager) nextCredentialPath(filename string) (string, error) {
	baseName := strings.TrimSuffix(filename, ".json")
	candidate := filepath.Join(m.authDir, filename)
	if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	}

	for i := 2; i <= 9999; i++ {
		name := fmt.Sprintf("%s-%d.json", baseName, i)
		path := filepath.Join(m.authDir, name)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}
	return "", errors.New("生成凭据文件名失败")
}

func (m *CodexSubscriptionManager) setSessionError(state, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[state]
	if !ok {
		return
	}
	session.Status = "error"
	session.ErrorMessage = strings.TrimSpace(message)
	session.UpdatedAt = time.Now().UTC()
}

func (m *CodexSubscriptionManager) completeSession(state string, credential *CodexSubscription) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[state]
	if !ok {
		return
	}
	session.Status = "ok"
	session.Credential = credential
	session.ErrorMessage = ""
	session.UpdatedAt = time.Now().UTC()
}

func (m *CodexSubscriptionManager) cleanupSessions() {
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	for state, session := range m.sessions {
		if session.Status == "pending" && now.After(session.ExpiresAt) {
			session.Status = "error"
			session.ErrorMessage = "授权已超时，请重新发起"
			session.UpdatedAt = now
		}
		if session.Status != "pending" && now.Sub(session.UpdatedAt) > codexSessionRetention {
			delete(m.sessions, state)
		}
	}
}

func buildCodexAuthorizeURL(clientID, redirectURI, state, challenge string) (string, error) {
	authURL := strings.TrimSpace(os.Getenv("CODEX_OAUTH_AUTH_URL"))
	if authURL == "" {
		authURL = defaultCodexOAuthAuthURL
	}

	scope := strings.TrimSpace(os.Getenv("CODEX_OAUTH_SCOPE"))
	if scope == "" {
		scope = defaultCodexOAuthScope
	}

	u, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", scope)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	if originator := strings.TrimSpace(os.Getenv("CODEX_OAUTH_ORIGINATOR")); originator != "" {
		query.Set("originator", originator)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func getCodexOAuthClientID() string {
	clientID := strings.TrimSpace(os.Getenv("CODEX_OAUTH_CLIENT_ID"))
	if clientID != "" {
		return clientID
	}
	return defaultCodexOAuthClientID
}

func BuildCodexOAuthRedirectURI(r *http.Request) string {
	if fromEnv := strings.TrimSpace(os.Getenv("CODEX_OAUTH_REDIRECT_URL")); fromEnv != "" {
		return fromEnv
	}
	_ = r
	return defaultCodexOAuthFixedRedirectURL
}

func shouldStartCodexCallbackForwarder() bool {
	if explicit := strings.TrimSpace(os.Getenv("CODEX_OAUTH_ENABLE_FORWARDER")); explicit != "" {
		enabled, err := strconv.ParseBool(explicit)
		if err != nil {
			slog.Warn("解析 CODEX_OAUTH_ENABLE_FORWARDER 失败，按 false 处理", "value", explicit, "error", err)
			return false
		}
		return enabled
	}

	return true
}

func GetCodexBackendBaseURL() string {
	if fromEnv := strings.TrimSpace(os.Getenv("CODEX_BACKEND_BASE_URL")); fromEnv != "" {
		return strings.TrimRight(fromEnv, "/")
	}
	return defaultCodexBackendBaseURL
}

func GetCodexClientVersion() string {
	return codexClientVersion
}

func GetCodexClientUserAgent() string {
	return codexClientUserAgent
}

func GetCodexClientOriginator() string {
	return codexClientOriginator
}

func collectQuotaFromHeaders(quota *CodexSubscriptionQuota, header http.Header) {
	if quota == nil {
		return
	}

	requestLimit := readRateLimitHeaderInt(header, "x-ratelimit-limit-requests", "x-openai-ratelimit-limit-requests")
	requestRemaining := readRateLimitHeaderInt(header, "x-ratelimit-remaining-requests", "x-openai-ratelimit-remaining-requests")
	requestReset := readRateLimitHeaderText(header, "x-ratelimit-reset-requests", "x-openai-ratelimit-reset-requests")
	requestResetAt, requestResetAtValue := parseQuotaResetAt(requestReset)

	tokenLimit := readRateLimitHeaderInt(header, "x-ratelimit-limit-tokens", "x-openai-ratelimit-limit-tokens")
	tokenRemaining := readRateLimitHeaderInt(header, "x-ratelimit-remaining-tokens", "x-openai-ratelimit-remaining-tokens")
	tokenReset := readRateLimitHeaderText(header, "x-ratelimit-reset-tokens", "x-openai-ratelimit-reset-tokens")
	tokenResetAt, tokenResetAtValue := parseQuotaResetAt(tokenReset)

	quota.RequestLimit = requestLimit
	quota.RequestRemaining = requestRemaining
	quota.RequestReset = requestReset
	quota.RequestResetAt = requestResetAtValue
	quota.TokenLimit = tokenLimit
	quota.TokenRemaining = tokenRemaining
	quota.TokenReset = tokenReset
	quota.TokenResetAt = tokenResetAtValue

	if !requestResetAt.IsZero() && (quota.ResetAt == "" || requestResetAt.Before(parseRFC3339OrZero(quota.ResetAt))) {
		quota.ResetAt = requestResetAt.Format(time.RFC3339)
		quota.ResetTime = requestReset
	}
	if !tokenResetAt.IsZero() {
		existsAt := parseRFC3339OrZero(quota.ResetAt)
		if existsAt.IsZero() || tokenResetAt.Before(existsAt) {
			quota.ResetAt = tokenResetAt.Format(time.RFC3339)
			quota.ResetTime = tokenReset
		}
	}

	if requestLimit != nil || requestRemaining != nil || tokenLimit != nil || tokenRemaining != nil || requestReset != "" || tokenReset != "" {
		quota.Source = joinQuotaSource(quota.Source, "response_headers")
	}

	planType := strings.TrimSpace(header.Get("x-codex-plan-type"))
	if planType != "" {
		quota.PlanType = planType
		quota.Source = joinQuotaSource(quota.Source, "codex_headers")
	}

	primaryUsed := readRateLimitHeaderFloat(header, "x-codex-primary-used-percent")
	if primaryUsed != nil {
		assignPercentQuota(primaryUsed, &quota.RequestLimit, &quota.RequestRemaining)
		quota.Source = joinQuotaSource(quota.Source, "codex_headers")
	}
	secondaryUsed := readRateLimitHeaderFloat(header, "x-codex-secondary-used-percent")
	if secondaryUsed != nil {
		assignPercentQuota(secondaryUsed, &quota.TokenLimit, &quota.TokenRemaining)
		quota.Source = joinQuotaSource(quota.Source, "codex_headers")
	}

	primaryResetAfter := readRateLimitHeaderInt(header, "x-codex-primary-reset-after-seconds")
	primaryResetAt := readRateLimitHeaderText(header, "x-codex-primary-reset-at")
	assignResetByCodexHeaders(quota, primaryResetAfter, primaryResetAt, true)

	secondaryResetAfter := readRateLimitHeaderInt(header, "x-codex-secondary-reset-after-seconds")
	secondaryResetAt := readRateLimitHeaderText(header, "x-codex-secondary-reset-at")
	assignResetByCodexHeaders(quota, secondaryResetAfter, secondaryResetAt, false)
}

func collectQuotaFromUsagePayload(quota *CodexSubscriptionQuota, rawBody []byte) {
	if quota == nil || len(rawBody) == 0 {
		return
	}

	var payload codexUsagePayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return
	}

	if planType := strings.TrimSpace(payload.PlanType); planType != "" {
		quota.PlanType = planType
		quota.Source = joinQuotaSource(quota.Source, "usage_api")
	}

	windows := buildCodexUsageWindows(payload)
	if len(windows) == 0 {
		return
	}

	quota.Windows = windows
	quota.Source = joinQuotaSource(quota.Source, "usage_api")
	assignLegacyQuotaFromWindows(quota, windows)
}

func collectQuotaFromBody(quota *CodexSubscriptionQuota, rawBody []byte) {
	if quota == nil || len(rawBody) == 0 {
		return
	}

	var body map[string]any
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return
	}

	if detail := castAnyToString(body["detail"]); detail != "" {
		quota.Message = detail
		quota.Source = joinQuotaSource(quota.Source, "response_body")
	}

	errNode, ok := body["error"].(map[string]any)
	if !ok {
		return
	}

	if msg := castAnyToString(errNode["message"]); msg != "" {
		quota.Message = msg
	}
	if resetTime := castAnyToString(errNode["reset_time"]); resetTime != "" {
		quota.ResetTime = resetTime
		if resetAt, text := parseQuotaResetAt(resetTime); !resetAt.IsZero() {
			quota.ResetAt = text
		}
	}
	if resetSeconds := castAnyToString(errNode["reset_seconds"]); resetSeconds != "" {
		if sec, err := strconv.ParseInt(resetSeconds, 10, 64); err == nil && sec > 0 {
			t := time.Now().UTC().Add(time.Duration(sec) * time.Second)
			quota.ResetAt = t.Format(time.RFC3339)
			if quota.ResetTime == "" {
				quota.ResetTime = (time.Duration(sec) * time.Second).String()
			}
		}
	}

	if quota.Message != "" || quota.ResetTime != "" || quota.ResetAt != "" {
		quota.Source = joinQuotaSource(quota.Source, "response_body")
	}
}

func normalizeCodexQuota(quota *CodexSubscriptionQuota) {
	if quota == nil {
		return
	}
	if quota.Message == "" && quota.HTTPStatus >= 400 {
		quota.Message = fmt.Sprintf("quota probe failed with status %d", quota.HTTPStatus)
	}
}

func readRateLimitHeaderInt(header http.Header, keys ...string) *int64 {
	for _, key := range keys {
		value := strings.TrimSpace(header.Get(key))
		if value == "" {
			continue
		}
		if parsed, ok := parseInt64Value(value); ok {
			return &parsed
		}
	}
	return nil
}

func readRateLimitHeaderText(header http.Header, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(header.Get(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func parseInt64Value(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return value, true
	}
	if value, err := strconv.ParseFloat(raw, 64); err == nil {
		return int64(value), true
	}
	return 0, false
}

func parseFloat64Value(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if value, err := strconv.ParseFloat(raw, 64); err == nil {
		return value, true
	}
	return 0, false
}

func readRateLimitHeaderFloat(header http.Header, keys ...string) *float64 {
	for _, key := range keys {
		value := strings.TrimSpace(header.Get(key))
		if value == "" {
			continue
		}
		if parsed, ok := parseFloat64Value(value); ok {
			return &parsed
		}
	}
	return nil
}

func parseQuotaResetAt(raw string) (time.Time, string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, ""
	}

	if duration, err := time.ParseDuration(value); err == nil {
		target := time.Now().UTC().Add(duration)
		return target, target.Format(time.RFC3339)
	}
	if unix, ok := parseInt64Value(value); ok {
		if len(value) > 10 {
			t := time.UnixMilli(unix).UTC()
			return t, t.Format(time.RFC3339)
		}
		t := time.Unix(unix, 0).UTC()
		return t, t.Format(time.RFC3339)
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RFC1123, time.RFC1123Z} {
		if t, err := time.Parse(layout, value); err == nil {
			t = t.UTC()
			return t, t.Format(time.RFC3339)
		}
	}
	return time.Time{}, ""
}

func parseRFC3339OrZero(raw string) time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	return time.Time{}
}

func joinQuotaSource(origin, next string) string {
	origin = strings.TrimSpace(origin)
	next = strings.TrimSpace(next)
	if next == "" {
		return origin
	}
	if origin == "" {
		return next
	}
	if strings.Contains(origin, next) {
		return origin
	}
	return origin + "+" + next
}

func buildCodexUsageWindows(payload codexUsagePayload) []CodexQuotaWindow {
	windows := make([]CodexQuotaWindow, 0, 4)
	windows = appendWindowsFromRateLimit(windows, payload.RateLimit, "", "主额度")
	windows = appendWindowsFromRateLimit(windows, payload.CodeReviewRateLimit, "code-review", "Code Review")
	return windows
}

func appendWindowsFromRateLimit(
	list []CodexQuotaWindow,
	limit *codexRateLimitInfo,
	idPrefix string,
	labelPrefix string,
) []CodexQuotaWindow {
	if limit == nil {
		return list
	}
	limitReached := false
	if limit.LimitReached != nil {
		limitReached = *limit.LimitReached
	}
	list = appendCodexQuotaWindow(
		list,
		withQuotaWindowPrefix(idPrefix, "primary"),
		buildQuotaWindowLabel(labelPrefix, "主窗口", limit.PrimaryWindow),
		limit.PrimaryWindow,
		limitReached,
		limit.Allowed,
	)
	list = appendCodexQuotaWindow(
		list,
		withQuotaWindowPrefix(idPrefix, "secondary"),
		buildQuotaWindowLabel(labelPrefix, "副窗口", limit.SecondaryWindow),
		limit.SecondaryWindow,
		limitReached,
		limit.Allowed,
	)
	return list
}

func withQuotaWindowPrefix(prefix, suffix string) string {
	prefix = strings.TrimSpace(prefix)
	suffix = strings.TrimSpace(suffix)
	if prefix == "" {
		return suffix
	}
	return prefix + "-" + suffix
}

func buildQuotaWindowLabel(prefix, role string, window *codexUsageWindow) string {
	role = strings.TrimSpace(role)
	if role == "" {
		role = "窗口"
	}
	label := role
	prefix = strings.TrimSpace(prefix)
	if prefix != "" {
		label = prefix + " " + role
	}
	if window != nil && window.LimitWindowSeconds != nil && *window.LimitWindowSeconds > 0 {
		if desc := formatQuotaWindowDuration(*window.LimitWindowSeconds); desc != "" {
			label = fmt.Sprintf("%s (%s)", label, desc)
		}
	}
	return label
}

func formatQuotaWindowDuration(seconds int64) string {
	switch {
	case seconds <= 0:
		return ""
	case seconds == codexFiveHourWindowSeconds:
		return "5 小时"
	case seconds == codexWeeklyWindowSeconds:
		return "7 天"
	case seconds%(24*3600) == 0:
		return fmt.Sprintf("%d 天", seconds/(24*3600))
	case seconds%3600 == 0:
		return fmt.Sprintf("%d 小时", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%d 分钟", seconds/60)
	default:
		return fmt.Sprintf("%d 秒", seconds)
	}
}

func appendCodexQuotaWindow(
	list []CodexQuotaWindow,
	id, label string,
	window *codexUsageWindow,
	limitReached bool,
	allowed *bool,
) []CodexQuotaWindow {
	if window == nil {
		return list
	}

	usedPercent := normalizePercentValue(window.UsedPercent)
	resetAt, resetLabel := resolveCodexWindowReset(window)
	isLimitReached := limitReached || (allowed != nil && !*allowed)
	if usedPercent == nil && isLimitReached && resetLabel != "-" {
		usedPercent = float64Ptr(100)
	}

	var remainingPercent *float64
	if usedPercent != nil {
		remaining := 100 - *usedPercent
		if remaining < 0 {
			remaining = 0
		}
		if remaining > 100 {
			remaining = 100
		}
		remainingPercent = &remaining
	}

	list = append(list, CodexQuotaWindow{
		ID:                 id,
		Label:              label,
		UsedPercent:        usedPercent,
		RemainingPercent:   remainingPercent,
		LimitWindowSeconds: cloneInt64Ptr(window.LimitWindowSeconds),
		ResetAfterSeconds:  cloneInt64Ptr(window.ResetAfterSeconds),
		ResetAt:            resetAt,
		ResetLabel:         resetLabel,
	})
	return list
}

func resolveCodexWindowReset(window *codexUsageWindow) (string, string) {
	if window == nil {
		return "", "-"
	}
	if window.ResetAt != nil && *window.ResetAt > 0 {
		target := time.Unix(*window.ResetAt, 0).UTC()
		text := target.Format(time.RFC3339)
		return text, text
	}
	if window.ResetAfterSeconds != nil && *window.ResetAfterSeconds > 0 {
		target := time.Now().UTC().Add(time.Duration(*window.ResetAfterSeconds) * time.Second)
		text := target.Format(time.RFC3339)
		return text, text
	}
	return "", "-"
}

func normalizePercentValue(raw *float64) *float64 {
	if raw == nil {
		return nil
	}
	value := *raw
	if value < 0 {
		value = 0
	}
	if value > 100 {
		value = 100
	}
	return &value
}

func assignLegacyQuotaFromWindows(quota *CodexSubscriptionQuota, windows []CodexQuotaWindow) {
	if quota == nil || len(windows) == 0 {
		return
	}

	requestIdx, tokenIdx := -1, -1
	for i := range windows {
		windowID := windows[i].ID
		switch windowID {
		case "five-hour", "primary":
			if requestIdx == -1 {
				requestIdx = i
			}
		case "weekly", "secondary":
			if tokenIdx == -1 {
				tokenIdx = i
			}
		default:
			if requestIdx == -1 && strings.Contains(windowID, "primary") && !strings.Contains(windowID, "code-review") {
				requestIdx = i
			}
			if tokenIdx == -1 && strings.Contains(windowID, "secondary") && !strings.Contains(windowID, "code-review") {
				tokenIdx = i
			}
		}
	}
	if requestIdx == -1 {
		requestIdx = 0
	}
	if tokenIdx == -1 {
		for i := range windows {
			if i == requestIdx {
				continue
			}
			if !strings.Contains(windows[i].ID, "code-review") {
				tokenIdx = i
				break
			}
		}
	}
	if tokenIdx == -1 && len(windows) > 1 {
		if requestIdx == 0 {
			tokenIdx = 1
		} else {
			tokenIdx = 0
		}
	}

	applyLegacyQuotaFromWindow(&windows[requestIdx], &quota.RequestLimit, &quota.RequestRemaining, &quota.RequestReset, &quota.RequestResetAt)
	if tokenIdx >= 0 {
		applyLegacyQuotaFromWindow(&windows[tokenIdx], &quota.TokenLimit, &quota.TokenRemaining, &quota.TokenReset, &quota.TokenResetAt)
	}

	requestResetAt := parseRFC3339OrZero(quota.RequestResetAt)
	tokenResetAt := parseRFC3339OrZero(quota.TokenResetAt)
	switch {
	case !requestResetAt.IsZero() && (tokenResetAt.IsZero() || requestResetAt.Before(tokenResetAt)):
		quota.ResetAt = quota.RequestResetAt
		quota.ResetTime = quota.RequestReset
	case !tokenResetAt.IsZero():
		quota.ResetAt = quota.TokenResetAt
		quota.ResetTime = quota.TokenReset
	}
}

func applyLegacyQuotaFromWindow(window *CodexQuotaWindow, limit, remaining **int64, reset, resetAt *string) {
	if window == nil {
		return
	}
	*limit, *remaining = quotaFromRemainingPercent(window.RemainingPercent)
	if window.ResetAfterSeconds != nil && *window.ResetAfterSeconds > 0 {
		*reset = fmt.Sprintf("%ds", *window.ResetAfterSeconds)
	}
	*resetAt = window.ResetAt
}

func quotaFromRemainingPercent(remainingPercent *float64) (*int64, *int64) {
	if remainingPercent == nil {
		return nil, nil
	}
	remaining := *remainingPercent
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	limit := int64(100)
	remainingRounded := int64(math.Round(remaining))
	return &limit, &remainingRounded
}

func float64Ptr(value float64) *float64 {
	return &value
}

func cloneInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func assignPercentQuota(usedPercent *float64, limitTarget, remainingTarget **int64) {
	if usedPercent == nil || limitTarget == nil || remainingTarget == nil {
		return
	}
	used := *usedPercent
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}

	limit := int64(100)
	remaining := int64(math.Round(100 - used))
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}

	if *limitTarget == nil {
		*limitTarget = &limit
	}
	if *remainingTarget == nil {
		*remainingTarget = &remaining
	}
}

func assignResetByCodexHeaders(quota *CodexSubscriptionQuota, resetAfter *int64, resetAtRaw string, primary bool) {
	if quota == nil {
		return
	}

	var (
		targetReset   *string
		targetResetAt *string
	)
	if primary {
		targetReset = &quota.RequestReset
		targetResetAt = &quota.RequestResetAt
	} else {
		targetReset = &quota.TokenReset
		targetResetAt = &quota.TokenResetAt
	}

	if resetAfter != nil {
		if *targetReset == "" {
			*targetReset = fmt.Sprintf("%ds", *resetAfter)
		}
		if *targetResetAt == "" && *resetAfter > 0 {
			resetAt := time.Now().UTC().Add(time.Duration(*resetAfter) * time.Second).Format(time.RFC3339)
			*targetResetAt = resetAt
		}
	}

	if parsedAt, text := parseQuotaResetAt(resetAtRaw); !parsedAt.IsZero() {
		*targetResetAt = text
		if *targetReset == "" {
			remain := int64(time.Until(parsedAt).Seconds())
			if remain > 0 {
				*targetReset = fmt.Sprintf("%ds", remain)
			}
		}
	}

	if *targetReset != "" || *targetResetAt != "" {
		quota.Source = joinQuotaSource(quota.Source, "codex_headers")
	}

	candidateAt := parseRFC3339OrZero(*targetResetAt)
	existingAt := parseRFC3339OrZero(quota.ResetAt)
	if !candidateAt.IsZero() && (existingAt.IsZero() || candidateAt.Before(existingAt)) {
		quota.ResetAt = candidateAt.Format(time.RFC3339)
		quota.ResetTime = *targetReset
	}
}

func parseCodexIDTokenClaims(idToken string) (codexIDTokenClaims, error) {
	claims := codexIDTokenClaims{}
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return claims, errors.New("id_token 格式错误")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, err
	}

	decoded := make(map[string]any)
	if err := json.Unmarshal(payloadBytes, &decoded); err != nil {
		return claims, err
	}

	claims.Email = castAnyToString(decoded["email"])

	authClaims, ok := decoded["https://api.openai.com/auth"].(map[string]any)
	if ok {
		claims.AccountID = castAnyToString(authClaims["chatgpt_account_id"])
		claims.PlanType = castAnyToString(authClaims["chatgpt_plan_type"])
		claims.SubscriptionActiveStart = castAnyToString(authClaims["chatgpt_subscription_active_start"])
		claims.SubscriptionActiveUntil = castAnyToString(authClaims["chatgpt_subscription_active_until"])
	}

	// 兼容部分 token 将套餐字段放在顶层的情况。
	if claims.AccountID == "" {
		claims.AccountID = castAnyToString(decoded["chatgpt_account_id"])
	}
	if claims.PlanType == "" {
		claims.PlanType = castAnyToString(decoded["chatgpt_plan_type"])
	}
	if claims.SubscriptionActiveStart == "" {
		claims.SubscriptionActiveStart = castAnyToString(decoded["chatgpt_subscription_active_start"])
	}
	if claims.SubscriptionActiveUntil == "" {
		claims.SubscriptionActiveUntil = castAnyToString(decoded["chatgpt_subscription_active_until"])
	}

	return claims, nil
}

func castAnyToString(v any) string {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		if value == 0 {
			return "0"
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func buildCredentialFileName(claims codexIDTokenClaims) string {
	emailPart := sanitizeFilenameSegment(claims.Email)
	if emailPart == "" {
		emailPart = sanitizeFilenameSegment(claims.AccountID)
	}
	if emailPart == "" {
		emailPart = "unknown"
	}

	planPart := sanitizeFilenameSegment(claims.PlanType)
	name := "codex-" + emailPart
	if planPart != "" {
		name = name + "-" + planPart
		if planPart == "team" && strings.TrimSpace(claims.AccountID) != "" {
			sum := sha256.Sum256([]byte(claims.AccountID))
			teamSuffix := base64.RawURLEncoding.EncodeToString(sum[:])[:8]
			name = name + "-" + strings.ToLower(teamSuffix)
		}
	}
	return name + ".json"
}

func sanitizeFilenameSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "@", "-at-")
	value = codexCleanerPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return ""
	}
	return value
}

func isValidCodexState(state string) bool {
	return codexStatePattern.MatchString(strings.TrimSpace(state))
}

func generateRandomString(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	if length <= 0 {
		return "", errors.New("length must be positive")
	}
	buf := make([]byte, length)
	random := make([]byte, length)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(buf), nil
}

func buildPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func writeJSON0600(targetPath string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')

	tmp := fmt.Sprintf("%s.%d.tmp", targetPath, time.Now().UnixNano())
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, targetPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
