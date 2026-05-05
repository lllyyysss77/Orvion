package handler

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func GetVersion(c *gin.Context) {
	common.Success(c, consts.Version)
}

const (
	githubLatestTagURL        = "https://api.github.com/repos/raciott/llmio/tags?per_page=1"
	githubCommitAPIURLPattern = "https://api.github.com/repos/raciott/llmio/commits/%s"
	githubTagsPageURL         = "https://github.com/raciott/llmio/tags"
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

type githubTagItemResp struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type githubLatestTagInfo struct {
	TagName       string
	CommitSHA     string
	CommitMessage string
	HTMLURL       string
}

type githubCommitResp struct {
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message string `json:"message"`
	} `json:"commit"`
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

	latestTag, err := fetchLatestTagFromGitHub()
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
		Where("key = ?", models.KeyGitHubVersionCheck).
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
		// 避免因为 GitHub API 短暂失败导致黄点完全消失。
		if strings.EqualFold(strings.TrimSpace(currentVersion), "dev") {
			resp.HasUpdate = true
			resp.LatestVersion = "latest"
			resp.Release = &versionReleaseOverview{
				TagName:     "latest",
				Name:        "更新信息暂不可用",
				PublishedAt: "",
				HTMLURL:     githubTagsPageURL,
				Body:        "当前为 dev 版本，本次未能连接 GitHub API。你可以先点击“查看标签页”确认是否有新版本。",
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
	resp.Release = &versionReleaseOverview{
		TagName:     tagName,
		Name:        "最新标签 " + tagName,
		PublishedAt: "",
		HTMLURL:     strings.TrimSpace(latestTag.HTMLURL),
		Body:        buildTagUpdateBody(tagName, latestTag.CommitSHA, latestTag.CommitMessage),
	}
	resp.HasUpdate = isLatestVersionGreater(tagName, currentVersion)
	return resp
}

func fetchLatestTagFromGitHub() (*githubLatestTagInfo, error) {
	var payload []githubTagItemResp
	if err := getGitHubJSONWithRetry(githubLatestTagURL, &payload); err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return nil, nil
	}

	tagName := strings.TrimSpace(payload[0].Name)
	if tagName == "" {
		return nil, nil
	}
	commitSHA := strings.TrimSpace(payload[0].Commit.SHA)
	commitMessage, commitHTMLURL, err := fetchCommitMessageFromGitHub(commitSHA)
	if err != nil {
		slog.Warn("读取 GitHub commit message 失败", "tag", tagName, "sha", commitSHA, "error", err)
	}

	htmlURL := buildTagDetailURL(tagName)
	if strings.TrimSpace(commitHTMLURL) != "" {
		htmlURL = strings.TrimSpace(commitHTMLURL)
	}

	return &githubLatestTagInfo{
		TagName:       tagName,
		CommitSHA:     commitSHA,
		CommitMessage: strings.TrimSpace(commitMessage),
		HTMLURL:       htmlURL,
	}, nil
}

func fetchCommitMessageFromGitHub(commitSHA string) (string, string, error) {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return "", "", nil
	}

	endpoint := fmt.Sprintf(githubCommitAPIURLPattern, neturl.PathEscape(commitSHA))

	var payload githubCommitResp
	if err := getGitHubJSONWithRetry(endpoint, &payload); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(payload.Commit.Message), strings.TrimSpace(payload.HTMLURL), nil
}

func buildGitHubHTTPClient(timeout time.Duration, forceHTTP11 bool) *http.Client {
	if timeout <= 0 {
		timeout = githubReleaseTimeout
	}
	client := &http.Client{Timeout: timeout}

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return client
	}
	transport := defaultTransport.Clone()
	if forceHTTP11 {
		// 显式禁用 HTTP/2，确保该请求使用 HTTP/1.1。
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}

	proxyURL := strings.TrimSpace(os.Getenv("GITHUB_HTTP_PROXY"))
	if proxyURL != "" {
		proxyParsed, err := neturl.Parse(proxyURL)
		if err != nil {
			slog.Warn("解析 GitHub 代理地址失败", "error", err)
		} else {
			transport.Proxy = http.ProxyURL(proxyParsed)
		}
	}

	client.Transport = transport
	return client
}

func getGitHubJSONWithRetry(endpoint string, target any) error {
	var lastErr error
	client := buildGitHubHTTPClient(githubReleaseTimeout, shouldForceHTTP11ForGitHubEndpoint(endpoint))
	for attempt := 1; attempt <= githubRequestRetryMax; attempt++ {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
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
			lastErr = fmt.Errorf("github status=%d", res.StatusCode)
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
		lastErr = errors.New("unknown github request error")
	}
	return lastErr
}

func shouldForceHTTP11ForGitHubEndpoint(endpoint string) bool {
	parsed, err := neturl.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(parsed.Host), "api.github.com") {
		return false
	}
	// 仅对 commits 详情接口强制 HTTP/1.1。
	return strings.Contains(strings.ToLower(strings.TrimSpace(parsed.Path)), "/commits/")
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
	return "https://github.com/raciott/llmio/tree/" + neturl.PathEscape(tagName)
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
