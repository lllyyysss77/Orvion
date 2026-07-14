package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"github.com/racio/orvion/service"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

func GetVersion(c *gin.Context) {
	common.Success(c, consts.Version)
}

const (
	githubVersionServiceURL   = "https://young-poetry-afb0.hwt821096.workers.dev/api/github/version"
	githubTagsPageURL         = "https://github.com/raciott/Orvion/tags"
	githubReleaseTimeout      = 6 * time.Second
	githubReleaseRefreshEvery = 1 * time.Minute
	githubRequestRetryMax     = 3
	githubRequestRetryBase    = 350 * time.Millisecond
)

var (
	githubVersionCacheMu     sync.RWMutex
	githubVersionCacheResp   = defaultVersionUpdateCheckResp()
	githubVersionRefreshOnce sync.Once
)

type versionUpdateCheckResp struct {
	CurrentVersion      string                  `json:"currentVersion"`
	LatestVersion       string                  `json:"latestVersion,omitempty"`
	HasUpdate           bool                    `json:"hasUpdate"`
	Release             *versionReleaseOverview `json:"release,omitempty"`
	BackendFetchSuccess bool                    `json:"backendFetchSuccess"`
	SuggestBrowserFetch bool                    `json:"suggestBrowserFetch"`
	Disabled            bool                    `json:"disabled"`
	FetchSource         string                  `json:"fetchSource"`
}

type versionReleaseOverview struct {
	TagName     string `json:"tagName"`
	Name        string `json:"name"`
	PublishedAt string `json:"publishedAt"`
	HTMLURL     string `json:"htmlUrl"`
	Body        string `json:"body"`
}

type githubLatestTagInfo struct {
	TagName       string
	Title         string
	Description   string
	PublishedAt   string
	CommitSHA     string
	CommitMessage string
	HTMLURL       string
}

type githubVersionServiceResp struct {
	Success    bool   `json:"success"`
	Repository string `json:"repository"`
	Latest     *struct {
		Tag         string `json:"tag"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Message     string `json:"message"`
		PublishedAt string `json:"published_at"`
		CommitSHA   string `json:"commit_sha"`
		CommitURL   string `json:"commit_url"`
		SourceURL   string `json:"source_url"`
	} `json:"latest"`
}

type normalizedVersion struct {
	major int
	minor int
	patch int
	pre   string
	valid bool
}

func GetVersionUpdateCheck(c *gin.Context) {
	enabled, err := isGitHubVersionCheckEnabled(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, "Failed to load github version check config: "+err.Error())
		return
	}
	if !enabled {
		common.Success(c, disabledVersionUpdateCheckResp())
		return
	}

	resp := getVersionUpdateCacheSnapshot()
	common.Success(c, resp)
}

// StartGitHubVersionUpdateRefresher 启动 GitHub 版本检查后台刷新任务。
// 行为：启动时先拉取一次，随后每分钟刷新一次；接口只返回内存缓存。
func StartGitHubVersionUpdateRefresher(ctx context.Context) {
	githubVersionRefreshOnce.Do(func() {
		go func() {
			refreshGitHubVersionCache()
			ticker := time.NewTicker(githubReleaseRefreshEvery)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refreshGitHubVersionCache()
				}
			}
		}()
	})
}

func refreshGitHubVersionCache() {
	currentVersion := resolveCurrentVersion()
	enabled, cfgErr := isGitHubVersionCheckEnabled(context.Background())
	if cfgErr != nil {
		slog.Warn("读取 GitHub 版本检查配置失败", "error", cfgErr)
	}
	if cfgErr == nil && !enabled {
		setVersionUpdateCache(disabledVersionUpdateCheckResp())
		return
	}

	latestTag, err := fetchLatestVersionFromService()
	next := buildVersionUpdateCheckResp(currentVersion, latestTag, err)

	setVersionUpdateCache(next)

	if err != nil {
		slog.Warn("刷新 GitHub 版本缓存失败", "error", err)
	}
}

func setVersionUpdateCache(next versionUpdateCheckResp) {
	githubVersionCacheMu.Lock()
	githubVersionCacheResp = next
	githubVersionCacheMu.Unlock()
}

func getVersionUpdateCacheSnapshot() versionUpdateCheckResp {
	githubVersionCacheMu.RLock()
	defer githubVersionCacheMu.RUnlock()
	return githubVersionCacheResp
}

func defaultVersionUpdateCheckResp() versionUpdateCheckResp {
	return buildVersionUpdateCheckResp(resolveCurrentVersion(), nil, errors.New("github version cache not ready"))
}

func disabledVersionUpdateCheckResp() versionUpdateCheckResp {
	return versionUpdateCheckResp{
		CurrentVersion:      resolveCurrentVersion(),
		HasUpdate:           false,
		BackendFetchSuccess: false,
		SuggestBrowserFetch: false,
		Disabled:            true,
		FetchSource:         "disabled",
	}
}

func isGitHubVersionCheckEnabled(ctx context.Context) (bool, error) {
	if models.DB == nil {
		return true, nil
	}

	config, err := gorm.G[models.Config](models.DB).
		Where(models.ColumnEquals("key"), models.KeyGitHubVersionCheck).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return true, nil
		}
		return true, err
	}

	raw := strings.TrimSpace(config.Value)
	if raw == "" {
		return true, nil
	}

	var cfg models.GitHubVersionCheckConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return true, fmt.Errorf("解析 GitHub 更新检查配置失败: %w", err)
	}
	return cfg.Enabled, nil
}

func resolveCurrentVersion() string {
	currentVersion := strings.TrimSpace(consts.Version)
	if currentVersion == "" {
		return "dev"
	}
	return currentVersion
}

func buildVersionUpdateCheckResp(currentVersion string, latestTag *githubLatestTagInfo, fetchErr error) versionUpdateCheckResp {
	resp := versionUpdateCheckResp{
		CurrentVersion:      currentVersion,
		HasUpdate:           false,
		BackendFetchSuccess: false,
		SuggestBrowserFetch: false,
		FetchSource:         "backend",
	}

	if fetchErr != nil {
		resp.SuggestBrowserFetch = true
		// 本地开发场景常见网络不可达；当前为 dev 时保留更新提示，
		// 避免因为版本服务短暂失败导致黄点完全消失。
		if strings.EqualFold(strings.TrimSpace(currentVersion), "dev") {
			resp.HasUpdate = true
			resp.LatestVersion = "latest"
			resp.Release = &versionReleaseOverview{
				TagName:     "latest",
				Name:        "更新信息暂不可用",
				PublishedAt: "",
				HTMLURL:     githubTagsPageURL,
				Body:        "当前为 dev 版本，本次未能连接版本检查服务。你可以先点击“查看标签页”确认是否有新版本。",
			}
		}
		return resp
	}

	resp.BackendFetchSuccess = true
	if latestTag == nil || strings.TrimSpace(latestTag.TagName) == "" {
		return resp
	}

	tagName := strings.TrimSpace(latestTag.TagName)
	resp.LatestVersion = tagName
	name := strings.TrimSpace(latestTag.Title)
	if name == "" {
		name = "最新标签 " + tagName
	}
	body := strings.TrimSpace(latestTag.Description)
	if body == "" {
		body = buildTagUpdateBody(tagName, latestTag.CommitSHA, latestTag.CommitMessage)
	}
	resp.Release = &versionReleaseOverview{
		TagName:     tagName,
		Name:        name,
		PublishedAt: strings.TrimSpace(latestTag.PublishedAt),
		HTMLURL:     strings.TrimSpace(latestTag.HTMLURL),
		Body:        body,
	}
	resp.HasUpdate = isLatestVersionGreater(tagName, currentVersion)
	return resp
}

func fetchLatestVersionFromService() (*githubLatestTagInfo, error) {
	var payload githubVersionServiceResp
	if err := getVersionServiceJSONWithRetry(githubVersionServiceURL, &payload); err != nil {
		return nil, err
	}
	return latestTagInfoFromServiceResp(payload)
}

func latestTagInfoFromServiceResp(payload githubVersionServiceResp) (*githubLatestTagInfo, error) {
	if !payload.Success {
		return nil, errors.New("version service returned success=false")
	}
	if payload.Latest == nil {
		return nil, nil
	}

	tagName := strings.TrimSpace(payload.Latest.Tag)
	if tagName == "" {
		return nil, nil
	}
	htmlURL := strings.TrimSpace(payload.Latest.SourceURL)
	if htmlURL == "" {
		htmlURL = strings.TrimSpace(payload.Latest.CommitURL)
	}
	if htmlURL == "" {
		htmlURL = buildTagDetailURL(tagName)
	}

	return &githubLatestTagInfo{
		TagName:       tagName,
		Title:         strings.TrimSpace(payload.Latest.Title),
		Description:   strings.TrimSpace(payload.Latest.Description),
		PublishedAt:   strings.TrimSpace(payload.Latest.PublishedAt),
		CommitSHA:     strings.TrimSpace(payload.Latest.CommitSHA),
		CommitMessage: strings.TrimSpace(payload.Latest.Message),
		HTMLURL:       htmlURL,
	}, nil
}

func buildVersionCheckHTTPClient(timeout time.Duration, proxyURL string) (*http.Client, error) {
	if timeout <= 0 {
		timeout = githubReleaseTimeout
	}
	baseClient, err := providers.GetClientWithProxy(timeout, strings.TrimSpace(proxyURL))
	if err != nil {
		return nil, err
	}
	client := *baseClient
	client.Timeout = timeout
	return &client, nil
}

func getVersionServiceJSONWithRetry(endpoint string, target any) error {
	directClient, err := buildVersionCheckHTTPClient(githubReleaseTimeout, "")
	if err != nil {
		return err
	}

	proxyURL := resolveVersionCheckProxyURL(context.Background())
	var proxyClient *http.Client
	if proxyURL != "" {
		proxyClient, err = buildVersionCheckHTTPClient(githubReleaseTimeout, proxyURL)
		if err != nil {
			slog.Warn("GitHub 版本检查全局代理配置无效，跳过代理重试", "proxy_url", proxyURL, "error", err)
		}
	}

	return getVersionServiceJSONWithFallback(endpoint, target, directClient, proxyClient)
}

func resolveVersionCheckProxyURL(ctx context.Context) string {
	cfg, found, err := service.LoadNetworkForwardingConfig(ctx)
	if err != nil {
		slog.Warn("读取版本检查全局代理配置失败", "error", err)
		return ""
	}
	if !found {
		return ""
	}
	return strings.TrimSpace(cfg.GlobalProxyURL)
}

func getVersionServiceJSONWithFallback(endpoint string, target any, directClient *http.Client, proxyClient *http.Client) error {
	directErr := getVersionServiceJSONWithClientRetry(endpoint, target, directClient)
	if directErr == nil {
		return nil
	}
	if proxyClient == nil {
		return directErr
	}

	slog.Warn("GitHub 版本检查直连失败，尝试代理获取", "url", endpoint, "error", directErr)
	proxyErr := getVersionServiceJSONWithClientRetry(endpoint, target, proxyClient)
	if proxyErr == nil {
		slog.Info("GitHub 版本检查已通过代理获取", "url", endpoint)
		return nil
	}
	return fmt.Errorf("版本检查直连失败: %w; 代理失败: %v", directErr, proxyErr)
}

func getVersionServiceJSONWithClientRetry(endpoint string, target any, client *http.Client) error {
	if client == nil {
		return errors.New("version check HTTP client is nil")
	}
	var lastErr error
	for attempt := 1; attempt <= githubRequestRetryMax; attempt++ {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "orvion-version-check")

		res, err := client.Do(req)
		if err != nil {
			lastErr = err
			if !shouldRetryGitHubError(err) || attempt >= githubRequestRetryMax {
				return err
			}
			time.Sleep(githubRequestRetryBase * time.Duration(attempt))
			continue
		}

		if res.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("version service status=%d", res.StatusCode)
			_ = res.Body.Close()
			if !shouldRetryGitHubStatus(res.StatusCode) || attempt >= githubRequestRetryMax {
				return lastErr
			}
			time.Sleep(githubRequestRetryBase * time.Duration(attempt))
			continue
		}

		decodeErr := json.NewDecoder(res.Body).Decode(target)
		_ = res.Body.Close()
		if decodeErr != nil {
			lastErr = decodeErr
			if !shouldRetryGitHubError(decodeErr) || attempt >= githubRequestRetryMax {
				return decodeErr
			}
			time.Sleep(githubRequestRetryBase * time.Duration(attempt))
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("unknown version service request error")
	}
	return lastErr
}

func shouldRetryGitHubStatus(statusCode int) bool {
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests {
		return true
	}
	return statusCode >= http.StatusInternalServerError
}

func shouldRetryGitHubError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "eof") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "timeout") ||
		strings.Contains(text, "stream error")
}

func buildTagDetailURL(tagName string) string {
	tagName = strings.TrimSpace(tagName)
	if tagName == "" {
		return githubTagsPageURL
	}
	return "https://github.com/raciott/Orvion/tree/" + neturl.PathEscape(tagName)
}

func buildTagUpdateBody(tagName string, commitSHA string, commitMessage string) string {
	tagName = strings.TrimSpace(tagName)
	commitSHA = strings.TrimSpace(commitSHA)
	commitMessage = strings.TrimSpace(commitMessage)
	if commitMessage != "" {
		return commitMessage
	}
	if tagName == "" {
		return "检测到新标签（当前仓库未发布 Release，版本检查基于 Tags）。"
	}
	if commitSHA != "" {
		shortSHA := commitSHA
		if len(shortSHA) > 12 {
			shortSHA = shortSHA[:12]
		}
		return fmt.Sprintf("检测到新标签：%s\n提交：%s\n（当前仓库未发布 Release，版本检查基于 Tags）", tagName, shortSHA)
	}
	return fmt.Sprintf("检测到新标签：%s\n（当前仓库未发布 Release，版本检查基于 Tags）", tagName)
}

func isLatestVersionGreater(latest string, current string) bool {
	// 明确产品规则：当前为 dev 时，只要 GitHub 有 tag（latest 非空）就提示更新。
	if strings.EqualFold(strings.TrimSpace(current), "dev") {
		return strings.TrimSpace(latest) != ""
	}

	latestVer := parseNormalizedVersion(latest)
	currentVer := parseNormalizedVersion(current)

	// 当前版本为其它非标准版本时，只要 latest 可解析就提示更新。
	if !currentVer.valid {
		return latestVer.valid
	}
	if !latestVer.valid {
		return false
	}

	if latestVer.major != currentVer.major {
		return latestVer.major > currentVer.major
	}
	if latestVer.minor != currentVer.minor {
		return latestVer.minor > currentVer.minor
	}
	if latestVer.patch != currentVer.patch {
		return latestVer.patch > currentVer.patch
	}

	latestHasPre := latestVer.pre != ""
	currentHasPre := currentVer.pre != ""
	if latestHasPre != currentHasPre {
		// 同主次修订下，正式版 > 预发布版。
		return !latestHasPre
	}
	if latestHasPre && currentHasPre {
		return latestVer.pre > currentVer.pre
	}
	return false
}

func parseNormalizedVersion(raw string) normalizedVersion {
	value := strings.TrimSpace(raw)
	if value == "" {
		return normalizedVersion{}
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "v"), "V")
	if value == "" {
		return normalizedVersion{}
	}

	parts := strings.SplitN(value, "+", 2)
	mainPart := strings.TrimSpace(parts[0])
	if mainPart == "" {
		return normalizedVersion{}
	}

	pre := ""
	mainAndPre := strings.SplitN(mainPart, "-", 2)
	core := strings.TrimSpace(mainAndPre[0])
	if len(mainAndPre) == 2 {
		pre = strings.TrimSpace(mainAndPre[1])
	}
	if core == "" {
		return normalizedVersion{}
	}

	nums := strings.Split(core, ".")
	if len(nums) < 2 || len(nums) > 3 {
		return normalizedVersion{}
	}
	if len(nums) == 2 {
		nums = append(nums, "0")
	}

	major, err := strconv.Atoi(nums[0])
	if err != nil || major < 0 {
		return normalizedVersion{}
	}
	minor, err := strconv.Atoi(nums[1])
	if err != nil || minor < 0 {
		return normalizedVersion{}
	}
	patch, err := strconv.Atoi(nums[2])
	if err != nil || patch < 0 {
		return normalizedVersion{}
	}

	return normalizedVersion{
		major: major,
		minor: minor,
		patch: patch,
		pre:   strings.ToLower(pre),
		valid: true,
	}
}
