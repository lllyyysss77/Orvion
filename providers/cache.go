package providers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type clientKey struct {
	timeout  time.Duration
	proxyURL string
}

type clientCache struct {
	mu      sync.RWMutex
	clients map[clientKey]*http.Client
}

var cache = &clientCache{
	clients: make(map[clientKey]*http.Client),
}

var dialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
}

// GetClientWithProxy 返回按 timeout + proxyURL 缓存复用的客户端。
// proxyURL 为空时直连（不走环境变量代理）。
func GetClientWithProxy(responseHeaderTimeout time.Duration, proxyURL string) (*http.Client, error) {
	normalizedProxy, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	key := clientKey{timeout: responseHeaderTimeout, proxyURL: normalizedProxy}

	cache.mu.RLock()
	if client, exists := cache.clients[key]; exists {
		cache.mu.RUnlock()
		return client, nil
	}
	cache.mu.RUnlock()

	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Double-check after acquiring write lock
	if client, exists := cache.clients[key]; exists {
		return client, nil
	}

	transport := &http.Transport{
		Proxy:                 nil, // 未配置代理时显式直连
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	transport.DialContext = dialer.DialContext

	if normalizedProxy != "" {
		parsedProxyURL, parseErr := url.Parse(normalizedProxy)
		if parseErr != nil {
			return nil, parseErr
		}
		switch strings.ToLower(parsedProxyURL.Scheme) {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsedProxyURL)
		case "socks5":
			socksDialer, buildErr := xproxy.FromURL(parsedProxyURL, dialer)
			if buildErr != nil {
				return nil, buildErr
			}
			if contextDialer, ok := socksDialer.(xproxy.ContextDialer); ok {
				transport.DialContext = contextDialer.DialContext
			} else {
				transport.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
					return socksDialer.Dial(network, addr)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported proxy scheme: %s", parsedProxyURL.Scheme)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   0, // No overall timeout, let ResponseHeaderTimeout control header timing
	}

	cache.clients[key] = client
	return client, nil
}

// ResetHTTPClientCache 关闭缓存客户端的空闲连接，并清空缓存。
func ResetHTTPClientCache() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	count := len(cache.clients)
	for _, client := range cache.clients {
		closeHTTPClientIdleConnections(client)
	}
	cache.clients = make(map[clientKey]*http.Client)
	return count
}

func closeHTTPClientIdleConnections(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	transport, ok := client.Transport.(interface{ CloseIdleConnections() })
	if !ok {
		return
	}
	transport.CloseIdleConnections()
}

func normalizeProxyURL(proxyURL string) (string, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return "", nil
	}

	lower := strings.ToLower(proxyURL)
	if strings.HasPrefix(lower, "socket5://") {
		proxyURL = "socks5://" + proxyURL[len("socket5://"):]
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	switch scheme {
	case "http", "https", "socks5":
		return proxyURL, nil
	default:
		return "", fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}
}
