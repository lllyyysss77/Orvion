package admin

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	"gorm.io/gorm"
)

const proxyRegionCheckTimeout = 20 * time.Second

type ProxyRegionCheckResult struct {
	IP          string    `json:"ip"`
	Country     string    `json:"country"`
	CountryCode string    `json:"country_code"`
	Region      string    `json:"region"`
	City        string    `json:"city"`
	CheckedAt   time.Time `json:"checked_at"`
	Error       string    `json:"error,omitempty"`
}

func sanitizeManagedProxyURL(raw string) (string, error) {
	proxyURL := strings.TrimSpace(raw)
	if proxyURL == "" {
		return "", errors.New("代理地址不能为空")
	}
	if strings.HasPrefix(strings.ToLower(proxyURL), "socket5://") {
		proxyURL = "socks5://" + proxyURL[len("socket5://"):]
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", errors.New("代理地址必须包含主机")
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "socks5":
		return proxyURL, nil
	default:
		return "", errors.New("仅支持 http 或 socks5 代理")
	}
}

func ensureProxyNameAvailable(ctx context.Context, name string, excludeID uint) error {
	query := models.DB.WithContext(ctx).Model(&models.Proxy{}).
		Where("LOWER(name) = ?", strings.ToLower(name))
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("节点名称已存在")
	}
	return nil
}

func GetProxies(c *gin.Context) {
	ctx := c.Request.Context()
	var proxies []models.Proxy
	if err := models.DB.WithContext(ctx).Order("created_at ASC").Order("id ASC").Find(&proxies).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}

	type proxyUsageRow struct {
		ProxyID    uint  `gorm:"column:proxy_id"`
		UsageCount int64 `gorm:"column:usage_count"`
	}
	var usageRows []proxyUsageRow
	if err := models.DB.WithContext(ctx).Model(&models.Provider{}).
		Select("proxy_id, COUNT(*) AS usage_count").
		Where("proxy_id > 0").
		Group("proxy_id").
		Scan(&usageRows).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	usageByProxyID := make(map[uint]int64, len(usageRows))
	for _, row := range usageRows {
		usageByProxyID[row.ProxyID] = row.UsageCount
	}

	items := make([]ProxyListItem, 0, len(proxies))
	for _, proxy := range proxies {
		items = append(items, ProxyListItem{Proxy: proxy, UsageCount: usageByProxyID[proxy.ID]})
	}
	common.Success(c, items)
}

func CreateProxy(c *gin.Context) {
	var req ProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		common.BadRequest(c, "代理名称不能为空")
		return
	}
	if err := ensureProxyNameAvailable(c.Request.Context(), name, 0); err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	proxyURL, err := sanitizeManagedProxyURL(req.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Invalid proxy_url: "+err.Error())
		return
	}

	proxy := models.Proxy{Name: name, ProxyURL: proxyURL}
	if err := models.DB.WithContext(c.Request.Context()).Create(&proxy).Error; err != nil {
		common.BadRequest(c, "Failed to create proxy: "+err.Error())
		return
	}
	common.Success(c, proxy)
}

func UpdateProxy(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}
	var req ProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		common.BadRequest(c, "代理名称不能为空")
		return
	}
	proxyURL, err := sanitizeManagedProxyURL(req.ProxyURL)
	if err != nil {
		common.BadRequest(c, "Invalid proxy_url: "+err.Error())
		return
	}

	ctx := c.Request.Context()
	existingProxy, err := gorm.G[models.Proxy](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Proxy not found")
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	if err := ensureProxyNameAvailable(ctx, name, uint(id)); err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	err = models.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"name":      name,
			"proxy_url": proxyURL,
		}
		if proxyURL != existingProxy.ProxyURL {
			updates["exit_ip"] = ""
			updates["exit_country"] = ""
			updates["exit_country_code"] = ""
			updates["exit_region"] = ""
			updates["exit_city"] = ""
			updates["region_checked_at"] = nil
			updates["region_check_error"] = ""
		}
		if err := tx.Model(&models.Proxy{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&models.Provider{}).Where("proxy_id = ?", id).Update("proxy_url", proxyURL).Error
	})
	if err != nil {
		common.BadRequest(c, "Failed to update proxy: "+err.Error())
		return
	}
	updated, err := gorm.G[models.Proxy](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, updated)
}

func DeleteProxy(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}
	ctx := c.Request.Context()
	var usageCount int64
	if err := models.DB.WithContext(ctx).Model(&models.Provider{}).Where("proxy_id = ?", id).Count(&usageCount).Error; err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if usageCount > 0 {
		common.BadRequest(c, "代理正在被提供商使用，请先解除关联")
		return
	}
	result, err := gorm.G[models.Proxy](models.DB).Where("id = ?", id).Delete(ctx)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if result == 0 {
		common.NotFound(c, "Proxy not found")
		return
	}
	common.Success(c, nil)
}

func CheckProxyRegion(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.BadRequest(c, "Invalid ID format")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), proxyRegionCheckTimeout)
	defer cancel()
	proxy, err := gorm.G[models.Proxy](models.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.NotFound(c, "Proxy not found")
			return
		}
		common.InternalServerError(c, err.Error())
		return
	}
	client, err := providers.GetClientWithProxy(providerProxyExitLookupTimeout, proxy.ProxyURL)
	if err != nil {
		result, saveErr := saveProxyRegionCheck(ctx, proxy.ID, providerProxyExitInfo{}, err)
		if saveErr != nil {
			common.InternalServerError(c, saveErr.Error())
			return
		}
		common.Success(c, result)
		return
	}
	info, err := lookupProviderProxyExit(ctx, client)
	result, saveErr := saveProxyRegionCheck(ctx, proxy.ID, info, err)
	if saveErr != nil {
		common.InternalServerError(c, saveErr.Error())
		return
	}
	common.Success(c, result)
}

func saveProxyRegionCheck(ctx context.Context, proxyID uint, info providerProxyExitInfo, checkErr error) (ProxyRegionCheckResult, error) {
	checkedAt := time.Now()
	result := ProxyRegionCheckResult{
		IP:          info.IP,
		Country:     info.Country,
		CountryCode: info.CountryCode,
		Region:      info.Region,
		City:        info.City,
		CheckedAt:   checkedAt,
	}
	if checkErr != nil {
		result.IP = ""
		result.Country = ""
		result.CountryCode = ""
		result.Region = ""
		result.City = ""
		result.Error = checkErr.Error()
	}
	err := models.DB.WithContext(ctx).Model(&models.Proxy{}).Where("id = ?", proxyID).Updates(map[string]any{
		"exit_ip":            result.IP,
		"exit_country":       result.Country,
		"exit_country_code":  result.CountryCode,
		"exit_region":        result.Region,
		"exit_city":          result.City,
		"region_checked_at":  checkedAt,
		"region_check_error": result.Error,
	}).Error
	return result, err
}
