package models

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

func ResolveProviderProxyURL(ctx context.Context, provider *Provider) error {
	if provider == nil || provider.ProxyID == 0 {
		return nil
	}
	proxy, err := gorm.G[Proxy](DB).Where("id = ?", provider.ProxyID).First(ctx)
	if err != nil {
		return fmt.Errorf("resolve provider proxy %d: %w", provider.ProxyID, err)
	}
	provider.ProxyURL = proxy.ProxyURL
	return nil
}

func ResolveProviderProxyURLs(ctx context.Context, providers []Provider) error {
	proxyIDs := make([]uint, 0, len(providers))
	seen := make(map[uint]struct{})
	for _, provider := range providers {
		if provider.ProxyID == 0 {
			continue
		}
		if _, ok := seen[provider.ProxyID]; ok {
			continue
		}
		seen[provider.ProxyID] = struct{}{}
		proxyIDs = append(proxyIDs, provider.ProxyID)
	}
	if len(proxyIDs) == 0 {
		return nil
	}

	var proxies []Proxy
	if err := DB.WithContext(ctx).Where("id IN ?", proxyIDs).Find(&proxies).Error; err != nil {
		return err
	}
	proxyURLs := make(map[uint]string, len(proxies))
	for _, proxy := range proxies {
		proxyURLs[proxy.ID] = proxy.ProxyURL
	}
	for i := range providers {
		if providers[i].ProxyID == 0 {
			continue
		}
		proxyURL, ok := proxyURLs[providers[i].ProxyID]
		if !ok {
			return fmt.Errorf("resolve provider proxy %d: %w", providers[i].ProxyID, gorm.ErrRecordNotFound)
		}
		providers[i].ProxyURL = proxyURL
	}
	return nil
}
