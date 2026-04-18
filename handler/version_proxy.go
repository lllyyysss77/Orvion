package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const (
	envGitHubHTTPProxy     = "GITHUB_HTTP_PROXY"
	envGitHubVMessURL      = "GITHUB_VMESS_URL"
	envGitHubXrayBinPath   = "GITHUB_XRAY_BIN"
	envGitHubXraySocksPort = "GITHUB_XRAY_SOCKS_PORT"

	// 默认 Trojan 候选（按顺序尝试）
	defaultGitHubTrojanJP01URL = "trojan://df6bba41-7fd6-4623-a80d-688fd3c11b99@pokemon-01.yunjnet.com:54225?security=tls&sni=www.apple.com.cn&allowInsecure=1&type=tcp&udp=1"
	defaultGitHubTrojanSG01URL = "trojan://df6bba41-7fd6-4623-a80d-688fd3c11b99@pokemon-01.yunjnet.com:55016?security=tls&sni=www.lamer.com.sg&allowInsecure=1&type=tcp&udp=1"
	defaultGitHubTrojanURL     = "trojan://df6bba41-7fd6-4623-a80d-688fd3c11b99@pokemon-01.yunjnet.com:56115?security=tls&sni=iosapps.itunes.apple.com&allowInsecure=1&type=tcp&udp=1"

	defaultGitHubXraySocksPort = 17890
	xrayBootTimeout            = 5 * time.Second
	proxyHealthCheckInterval   = 30 * time.Second
)

var gitHubProxyMgr = &vmessProxyManager{}
var proxyHealthLoopOnce sync.Once
var proxyHealthLoopStopOnce sync.Once
var proxyHealthLoopStopCh = make(chan struct{})

var proxyHealthCheckMu sync.Mutex
var proxyLastCheckedAt time.Time

var proxyWarmupProbeTargets = []string{
	"https://api.github.com",
	"https://google.com",
}

var proxyHealthRequiredTargets = []string{
	"https://api.github.com",
}

type proxyCandidate struct {
	name string
	link string
}

var defaultProxyCandidates = []proxyCandidate{
	{
		name: "trojan-jp01-3x",
		link: defaultGitHubTrojanJP01URL,
	},
	{
		name: "trojan-sg01-3x",
		link: defaultGitHubTrojanSG01URL,
	},
	{
		name: "trojan-fallback",
		link: defaultGitHubTrojanURL,
	},
}

type vmessProxyManager struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	config   string
	proxyURL string
	lastLink string
}

type vmessLinkConfig struct {
	Address string `json:"add"`
	Port    string `json:"port"`
	ID      string `json:"id"`
	AlterID string `json:"aid"`
	Net     string `json:"net"`
	Type    string `json:"type"`
	Host    string `json:"host"`
	Path    string `json:"path"`
	TLS     string `json:"tls"`
	SNI     string `json:"sni"`
	ALPN    string `json:"alpn"`
}

type trojanLinkConfig struct {
	Address       string
	Port          string
	Password      string
	SNI           string
	ALPN          string
	Net           string
	Host          string
	Path          string
	Security      string
	AllowInsecure bool
	UDP           bool
}

type xrayConfig struct {
	Log       map[string]any `json:"log,omitempty"`
	Inbounds  []any          `json:"inbounds"`
	Outbounds []any          `json:"outbounds"`
}

// StartGitHubVMessProxyWarmup 在进程启动阶段预热内置代理。
// 仅在设置了 GITHUB_VMESS_URL 且未设置 GITHUB_HTTP_PROXY 时尝试启动。
func StartGitHubVMessProxyWarmup() {
	if strings.TrimSpace(os.Getenv(envGitHubHTTPProxy)) != "" {
		return
	}
	startGitHubProxyHealthLoop()
	proxyURL, selected, err := ensureGitHubProxyCandidates()
	if err != nil {
		slog.Warn("预热内置代理失败", "error", err)
		return
	}
	slog.Info("内置代理已就绪", "proxy_url", proxyURL, "candidate", selected.name)
	probeProxyWarmupTargets(proxyURL)
}

// StopGitHubVMessProxy 主动停止内置代理进程。
func StopGitHubVMessProxy() {
	proxyHealthLoopStopOnce.Do(func() {
		close(proxyHealthLoopStopCh)
	})
	gitHubProxyMgr.mu.Lock()
	defer gitHubProxyMgr.mu.Unlock()
	gitHubProxyMgr.stopLocked()
}

func buildGitHubHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = githubReleaseTimeout
	}
	client := &http.Client{Timeout: timeout}

	proxyURL := resolveGitHubProxyURL()
	if proxyURL == "" {
		return client
	}

	proxyParsed, err := neturl.Parse(proxyURL)
	if err != nil {
		slog.Warn("解析 GitHub 代理地址失败", "error", err)
		return client
	}

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return client
	}
	transport := defaultTransport.Clone()
	transport.Proxy = http.ProxyURL(proxyParsed)
	client.Transport = transport
	return client
}

func resolveGitHubProxyURL() string {
	if proxy := strings.TrimSpace(os.Getenv(envGitHubHTTPProxy)); proxy != "" {
		return proxy
	}

	proxyURL, _, err := ensureGitHubProxyCandidates()
	if err != nil {
		slog.Warn("启动内置代理失败，回退直连 GitHub", "error", err)
		return ""
	}
	return proxyURL
}

func ensureGitHubProxyCandidates() (string, proxyCandidate, error) {
	return ensureGitHubProxyCandidatesWithOptions(false)
}

func ensureGitHubProxyCandidatesWithOptions(forceHealthCheck bool) (string, proxyCandidate, error) {
	candidates := resolveProxyCandidates()
	var errorsText []string

	activeLink, activeProxyURL, activeOK := gitHubProxyMgr.current()
	if activeOK {
		activeCandidate := findProxyCandidateByLink(candidates, activeLink)
		if activeCandidate.name == "" {
			activeCandidate = proxyCandidate{name: "active-runtime-candidate", link: activeLink}
		}

		shouldCheck := forceHealthCheck || shouldCheckProxyHealthNow()
		if !shouldCheck {
			return activeProxyURL, activeCandidate, nil
		}
		if err := probeProxyHealth(activeProxyURL); err == nil {
			return activeProxyURL, activeCandidate, nil
		} else {
			slog.Warn("当前内置代理健康检查失败，准备切换候选", "candidate", activeCandidate.name, "error", err)
			gitHubProxyMgr.invalidateIfMatch(activeLink)
			candidates = rotateProxyCandidatesFromNext(candidates, activeLink)
			errorsText = append(errorsText, fmt.Sprintf("%s: %v", activeCandidate.name, err))
		}
	}

	for _, candidate := range candidates {
		proxyURL, reused, err := gitHubProxyMgr.ensure(candidate.link)
		if err != nil {
			errorsText = append(errorsText, fmt.Sprintf("%s: %v", candidate.name, err))
			continue
		}

		// 仅在首次拉起候选或定时检查窗口到达时做健康探测。
		shouldCheck := forceHealthCheck || !reused || shouldCheckProxyHealthNow()
		if !shouldCheck {
			return proxyURL, candidate, nil
		}

		if err := probeProxyHealth(proxyURL); err != nil {
			errorsText = append(errorsText, fmt.Sprintf("%s: %v", candidate.name, err))
			gitHubProxyMgr.invalidateIfMatch(candidate.link)
			continue
		}
		return proxyURL, candidate, nil
	}

	if len(errorsText) == 0 {
		return "", proxyCandidate{}, errors.New("无可用代理候选")
	}
	return "", proxyCandidate{}, fmt.Errorf("所有代理候选均不可用: %s", strings.Join(errorsText, " | "))
}

func shouldCheckProxyHealthNow() bool {
	proxyHealthCheckMu.Lock()
	defer proxyHealthCheckMu.Unlock()
	now := time.Now()
	if proxyLastCheckedAt.IsZero() || now.Sub(proxyLastCheckedAt) >= proxyHealthCheckInterval {
		proxyLastCheckedAt = now
		return true
	}
	return false
}

func findProxyCandidateByLink(candidates []proxyCandidate, link string) proxyCandidate {
	link = strings.TrimSpace(link)
	if link == "" {
		return proxyCandidate{}
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.link) == link {
			return candidate
		}
	}
	return proxyCandidate{}
}

func rotateProxyCandidatesFromNext(candidates []proxyCandidate, currentLink string) []proxyCandidate {
	if len(candidates) <= 1 {
		return candidates
	}
	currentLink = strings.TrimSpace(currentLink)
	currentIdx := -1
	for idx, candidate := range candidates {
		if strings.TrimSpace(candidate.link) == currentLink {
			currentIdx = idx
			break
		}
	}
	if currentIdx < 0 {
		return candidates
	}

	out := make([]proxyCandidate, 0, len(candidates))
	for i := 1; i <= len(candidates); i++ {
		out = append(out, candidates[(currentIdx+i)%len(candidates)])
	}
	return out
}

func startGitHubProxyHealthLoop() {
	proxyHealthLoopOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(proxyHealthCheckInterval)
			defer ticker.Stop()
			for {
				select {
				case <-proxyHealthLoopStopCh:
					return
				case <-ticker.C:
					if strings.TrimSpace(os.Getenv(envGitHubHTTPProxy)) != "" {
						continue
					}
					if _, _, err := ensureGitHubProxyCandidatesWithOptions(true); err != nil {
						slog.Warn("内置代理定时健康检查失败", "error", err)
					}
				}
			}
		}()
	})
}

func resolveProxyCandidates() []proxyCandidate {
	if link := strings.TrimSpace(os.Getenv(envGitHubVMessURL)); link != "" {
		return []proxyCandidate{
			{
				name: "env-github-vmess-url",
				link: link,
			},
		}
	}

	out := make([]proxyCandidate, 0, len(defaultProxyCandidates))
	for _, candidate := range defaultProxyCandidates {
		if strings.TrimSpace(candidate.link) == "" {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func (m *vmessProxyManager) ensure(proxyLink string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	proxyLink = strings.TrimSpace(proxyLink)
	if proxyLink == "" {
		return "", false, errors.New("代理链接为空")
	}

	if m.cmd != nil && m.cmd.ProcessState == nil && m.lastLink == proxyLink && m.proxyURL != "" {
		return m.proxyURL, true, nil
	}

	m.stopLocked()

	socksPort := parseXraySocksPort()
	binPath, err := resolveXrayBinaryPath()
	if err != nil {
		return "", false, err
	}

	configPath, err := writeXrayConfigFile(proxyLink, socksPort)
	if err != nil {
		return "", false, err
	}

	cmd, err := startXrayProcess(binPath, configPath, socksPort)
	if err != nil {
		_ = os.Remove(configPath)
		return "", false, err
	}

	m.cmd = cmd
	m.config = configPath
	m.lastLink = proxyLink
	m.proxyURL = fmt.Sprintf("socks5://127.0.0.1:%d", socksPort)
	return m.proxyURL, false, nil
}

func (m *vmessProxyManager) invalidateIfMatch(proxyLink string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(m.lastLink) == strings.TrimSpace(proxyLink) {
		m.stopLocked()
	}
}

func (m *vmessProxyManager) current() (string, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.ProcessState != nil || strings.TrimSpace(m.lastLink) == "" || strings.TrimSpace(m.proxyURL) == "" {
		return "", "", false
	}
	return m.lastLink, m.proxyURL, true
}

func (m *vmessProxyManager) stopLocked() {
	if m.cmd != nil && m.cmd.Process != nil && m.cmd.ProcessState == nil {
		_ = m.cmd.Process.Kill()
	}
	m.cmd = nil
	m.lastLink = ""
	m.proxyURL = ""
	if strings.TrimSpace(m.config) != "" {
		_ = os.Remove(m.config)
		m.config = ""
	}
}

func parseVMessLink(raw string) (*vmessLinkConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("vmess 链接为空")
	}
	if !strings.HasPrefix(strings.ToLower(raw), "vmess://") {
		return nil, errors.New("不是有效的 vmess:// 链接")
	}

	encoded := strings.TrimSpace(raw[len("vmess://"):])
	if encoded == "" {
		return nil, errors.New("vmess 内容为空")
	}

	decoded, err := decodeVMessBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf("vmess base64 解码失败: %w", err)
	}

	var cfg vmessLinkConfig
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return nil, fmt.Errorf("vmess JSON 解析失败: %w", err)
	}

	if strings.TrimSpace(cfg.Address) == "" || strings.TrimSpace(cfg.Port) == "" || strings.TrimSpace(cfg.ID) == "" {
		return nil, errors.New("vmess 缺少 add/port/id 关键字段")
	}
	return &cfg, nil
}

func parseTrojanLink(raw string) (*trojanLinkConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("trojan 链接为空")
	}

	parsed, err := neturl.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("trojan 链接解析失败: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "trojan") {
		return nil, errors.New("不是有效的 trojan:// 链接")
	}

	address := strings.TrimSpace(parsed.Hostname())
	port := strings.TrimSpace(parsed.Port())
	password := ""
	if parsed.User != nil {
		if pass, ok := parsed.User.Password(); ok && strings.TrimSpace(pass) != "" {
			password = strings.TrimSpace(pass)
		} else {
			password = strings.TrimSpace(parsed.User.Username())
		}
	}
	if address == "" || port == "" || password == "" {
		return nil, errors.New("trojan 缺少 address/port/password 关键字段")
	}

	query := parsed.Query()
	security := strings.ToLower(strings.TrimSpace(firstNonEmpty(query.Get("security"), query.Get("tls"))))
	if security == "" {
		security = "tls"
	}

	netType := strings.ToLower(strings.TrimSpace(firstNonEmpty(query.Get("type"), query.Get("net"))))
	if netType == "" {
		netType = "tcp"
	}

	return &trojanLinkConfig{
		Address:       address,
		Port:          port,
		Password:      password,
		SNI:           strings.TrimSpace(firstNonEmpty(query.Get("sni"), query.Get("peer"), query.Get("servername"))),
		ALPN:          strings.TrimSpace(query.Get("alpn")),
		Net:           netType,
		Host:          strings.TrimSpace(firstNonEmpty(query.Get("host"), query.Get("headerType"))),
		Path:          strings.TrimSpace(query.Get("path")),
		Security:      security,
		AllowInsecure: parseBoolLike(firstNonEmpty(query.Get("allowInsecure"), query.Get("skip-cert-verify"))),
		UDP:           parseBoolLike(firstNonEmpty(query.Get("udp"), query.Get("enable_udp"))),
	}, nil
}

func decodeVMessBase64(encoded string) ([]byte, error) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(encoded), "-", "+"), "_", "/")
	if mod := len(normalized) % 4; mod != 0 {
		normalized += strings.Repeat("=", 4-mod)
	}
	return base64.StdEncoding.DecodeString(normalized)
}

func parseBoolLike(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseXraySocksPort() int {
	raw := strings.TrimSpace(os.Getenv(envGitHubXraySocksPort))
	if raw == "" {
		return defaultGitHubXraySocksPort
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > 65535 {
		return defaultGitHubXraySocksPort
	}
	return value
}

func resolveXrayBinaryPath() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(envGitHubXrayBinPath)); custom != "" {
		if fileExists(custom) {
			return custom, nil
		}
		return "", fmt.Errorf("未找到 GITHUB_XRAY_BIN 指定文件: %s", custom)
	}

	candidates := make([]string, 0, 3)
	exePath, err := os.Executable()
	if err == nil && strings.TrimSpace(exePath) != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(exePath), xrayRelativePath()))
	}
	candidates = append(candidates, filepath.Join(".", xrayRelativePath()))
	candidates = append(candidates, filepath.Join(".", "bin", "xray"))

	for _, candidate := range candidates {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("未找到 xray-core 可执行文件，请放置到 %s 或设置 %s", xrayRelativePath(), envGitHubXrayBinPath)
}

func xrayRelativePath() string {
	binName := "xray"
	if runtime.GOOS == "windows" {
		binName = "xray.exe"
	}
	return filepath.Join("bin", "xray-core", runtime.GOOS+"-"+runtime.GOARCH, binName)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func writeXrayConfigFile(proxyLink string, socksPort int) (string, error) {
	configContent, err := buildXrayConfigJSON(proxyLink, socksPort)
	if err != nil {
		return "", err
	}

	file, err := os.CreateTemp("", "llmio-xray-*.json")
	if err != nil {
		return "", err
	}
	defer file.Close()

	if _, err := file.Write(configContent); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func buildXrayConfigJSON(proxyLink string, socksPort int) ([]byte, error) {
	outbound, enableUDP, err := buildOutboundConfig(proxyLink)
	if err != nil {
		return nil, err
	}

	config := xrayConfig{
		Log: map[string]any{
			"loglevel": "warning",
		},
		Inbounds: []any{
			map[string]any{
				"listen":   "127.0.0.1",
				"port":     socksPort,
				"protocol": "socks",
				"settings": map[string]any{
					"auth": "noauth",
					"udp":  enableUDP,
				},
			},
		},
		Outbounds: []any{
			outbound,
			map[string]any{
				"tag":      "direct",
				"protocol": "freedom",
			},
		},
	}

	return json.MarshalIndent(config, "", "  ")
}

func buildOutboundConfig(proxyLink string) (map[string]any, bool, error) {
	proxyLink = strings.TrimSpace(proxyLink)
	if proxyLink == "" {
		return nil, false, errors.New("代理链接为空")
	}

	switch {
	case strings.HasPrefix(strings.ToLower(proxyLink), "vmess://"):
		conf, err := parseVMessLink(proxyLink)
		if err != nil {
			return nil, false, err
		}
		outbound, err := buildVMessOutbound(conf)
		if err != nil {
			return nil, false, err
		}
		return outbound, false, nil
	case strings.HasPrefix(strings.ToLower(proxyLink), "trojan://"):
		conf, err := parseTrojanLink(proxyLink)
		if err != nil {
			return nil, false, err
		}
		outbound, err := buildTrojanOutbound(conf)
		if err != nil {
			return nil, false, err
		}
		return outbound, conf.UDP, nil
	default:
		return nil, false, errors.New("仅支持 vmess:// 或 trojan:// 链接")
	}
}

func buildVMessOutbound(link *vmessLinkConfig) (map[string]any, error) {
	serverPort, err := strconv.Atoi(strings.TrimSpace(link.Port))
	if err != nil || serverPort <= 0 || serverPort > 65535 {
		return nil, fmt.Errorf("vmess 端口无效: %s", strings.TrimSpace(link.Port))
	}
	alterID := 0
	if raw := strings.TrimSpace(link.AlterID); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			alterID = parsed
		}
	}

	network := strings.ToLower(strings.TrimSpace(link.Net))
	if network == "" {
		network = "tcp"
	}

	outbound := map[string]any{
		"protocol": "vmess",
		"settings": map[string]any{
			"vnext": []any{
				map[string]any{
					"address": strings.TrimSpace(link.Address),
					"port":    serverPort,
					"users": []any{
						map[string]any{
							"id":       strings.TrimSpace(link.ID),
							"alterId":  alterID,
							"security": "auto",
						},
					},
				},
			},
		},
	}

	streamSettings := map[string]any{
		"network": network,
	}

	if strings.EqualFold(strings.TrimSpace(link.TLS), "tls") {
		streamSettings["security"] = "tls"
		tlsSettings := map[string]any{}
		serverName := firstNonEmpty(strings.TrimSpace(link.SNI), firstHost(strings.TrimSpace(link.Host)))
		if serverName != "" {
			tlsSettings["serverName"] = serverName
		}
		alpnValues := splitAndTrim(strings.TrimSpace(link.ALPN), ",")
		if len(alpnValues) > 0 {
			tlsSettings["alpn"] = alpnValues
		}
		if len(tlsSettings) > 0 {
			streamSettings["tlsSettings"] = tlsSettings
		}
	} else {
		streamSettings["security"] = "none"
	}

	if network == "ws" {
		wsSettings := map[string]any{}
		if path := strings.TrimSpace(link.Path); path != "" {
			wsSettings["path"] = path
		}
		if host := firstHost(strings.TrimSpace(link.Host)); host != "" {
			wsSettings["headers"] = map[string]any{"Host": host}
		}
		if len(wsSettings) > 0 {
			streamSettings["wsSettings"] = wsSettings
		}
	}

	outbound["streamSettings"] = streamSettings
	return outbound, nil
}

func buildTrojanOutbound(link *trojanLinkConfig) (map[string]any, error) {
	serverPort, err := strconv.Atoi(strings.TrimSpace(link.Port))
	if err != nil || serverPort <= 0 || serverPort > 65535 {
		return nil, fmt.Errorf("trojan 端口无效: %s", strings.TrimSpace(link.Port))
	}

	network := strings.ToLower(strings.TrimSpace(link.Net))
	if network == "" {
		network = "tcp"
	}
	security := strings.ToLower(strings.TrimSpace(link.Security))
	if security == "" {
		security = "tls"
	}

	outbound := map[string]any{
		"protocol": "trojan",
		"settings": map[string]any{
			"servers": []any{
				map[string]any{
					"address":  strings.TrimSpace(link.Address),
					"port":     serverPort,
					"password": strings.TrimSpace(link.Password),
				},
			},
		},
	}

	streamSettings := map[string]any{
		"network":  network,
		"security": security,
	}

	if security == "tls" {
		tlsSettings := map[string]any{
			"allowInsecure": link.AllowInsecure,
		}
		if serverName := strings.TrimSpace(link.SNI); serverName != "" {
			tlsSettings["serverName"] = serverName
		}
		alpnValues := splitAndTrim(strings.TrimSpace(link.ALPN), ",")
		if len(alpnValues) > 0 {
			tlsSettings["alpn"] = alpnValues
		}
		streamSettings["tlsSettings"] = tlsSettings
	}

	if network == "ws" {
		wsSettings := map[string]any{}
		if path := strings.TrimSpace(link.Path); path != "" {
			wsSettings["path"] = path
		}
		if host := firstHost(strings.TrimSpace(link.Host)); host != "" {
			wsSettings["headers"] = map[string]any{"Host": host}
		}
		if len(wsSettings) > 0 {
			streamSettings["wsSettings"] = wsSettings
		}
	}

	outbound["streamSettings"] = streamSettings
	return outbound, nil
}

func startXrayProcess(binPath string, configPath string, socksPort int) (*exec.Cmd, error) {
	attempts := [][]string{
		{"run", "-config", configPath},
		{"-config", configPath},
	}

	var lastErr error
	for _, args := range attempts {
		cmd, err := startXrayOnce(binPath, args, socksPort)
		if err == nil {
			return cmd, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("未知错误")
	}
	return nil, fmt.Errorf("xray 启动失败: %w", lastErr)
}

func startXrayOnce(binPath string, args []string, socksPort int) (*exec.Cmd, error) {
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	if err := waitXraySocksReady(socksPort, done, xrayBootTimeout); err != nil {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
		return nil, err
	}

	go func() {
		if err := <-done; err != nil {
			slog.Warn("xray 进程退出", "error", err)
		}
	}()

	return cmd, nil
}

func waitXraySocksReady(port int, done <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err != nil {
				return fmt.Errorf("xray 提前退出: %w", err)
			}
			return errors.New("xray 已退出")
		default:
		}

		conn, err := net.DialTimeout("tcp", addr, 220*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("等待 xray SOCKS 端口就绪超时: %s", addr)
}

func splitAndTrim(raw string, sep string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, sep)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstHost(raw string) string {
	values := splitAndTrim(raw, ",")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func probeProxyWarmupTargets(proxyURL string) {
	_, _ = probeProxyTargets(proxyURL, proxyWarmupProbeTargets, true)
}

func probeProxyHealth(proxyURL string) error {
	failed, err := probeProxyTargets(proxyURL, proxyHealthRequiredTargets, false)
	if err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("健康探测失败: %s", strings.Join(failed, ", "))
	}
	return nil
}

func probeProxyTargets(proxyURL string, targets []string, withLog bool) ([]string, error) {
	client, err := newProxyProbeClient(proxyURL)
	if err != nil {
		if withLog {
			slog.Warn("创建代理探测客户端失败", "proxy_url", proxyURL, "error", err)
		}
		return nil, err
	}

	failed := make([]string, 0)
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		statusCode, reqErr := doProbeRequest(client, target)
		if reqErr != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", target, reqErr))
			if withLog {
				slog.Warn("代理探测失败", "target", target, "error", reqErr)
			}
			continue
		}
		if withLog {
			slog.Info("代理探测成功", "target", target, "status_code", statusCode)
		}
	}
	return failed, nil
}

func newProxyProbeClient(proxyURL string) (*http.Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, errors.New("代理地址为空")
	}

	parsedProxyURL, err := neturl.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理地址解析失败: %w", err)
	}

	dialer, err := xproxy.FromURL(parsedProxyURL, xproxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("代理拨号器创建失败: %w", err)
	}

	transport := &http.Transport{
		DialContext: func(_ context.Context, network string, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		DisableKeepAlives:     true,
	}

	return &http.Client{
		Timeout:   6 * time.Second,
		Transport: transport,
	}, nil
}

func doProbeRequest(client *http.Client, target string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "orvion-proxy-warmup")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
