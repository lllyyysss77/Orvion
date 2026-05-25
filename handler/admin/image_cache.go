package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/service"
)

func GetImageCache(c *gin.Context) {
	common.Success(c, service.ListTelegramStatusImageCache())
}

func GetImageCacheContent(c *gin.Context) {
	id, ok := parseImageCacheID(c)
	if !ok {
		return
	}

	content, err := service.GetTelegramStatusImageCacheContent(id)
	if err != nil {
		if errors.Is(err, service.ErrTelegramStatusImageCacheNotFound) {
			common.NotFound(c, "图片缓存不存在")
			return
		}
		common.InternalServerError(c, "读取图片缓存失败: "+err.Error())
		return
	}

	fileName := strings.TrimSpace(content.FileName)
	if fileName == "" {
		fileName = fmt.Sprintf("status_image_%d", id)
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, strings.ReplaceAll(fileName, `"`, "")))
	c.Data(http.StatusOK, content.MIMEType, content.Binary)
}

func DeleteImageCache(c *gin.Context) {
	id, ok := parseImageCacheID(c)
	if !ok {
		return
	}

	if !service.DeleteTelegramStatusImageCacheItem(id) {
		common.NotFound(c, "图片缓存不存在")
		return
	}
	common.Success(c, gin.H{"id": id})
}

func parseImageCacheID(c *gin.Context) (uint64, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		common.BadRequest(c, "图片缓存 ID 无效")
		return 0, false
	}
	return id, true
}
