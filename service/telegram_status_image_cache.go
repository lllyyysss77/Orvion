package service

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// TelegramStatusImageCacheItem 是系统状态图片缓存的前端展示摘要。
type TelegramStatusImageCacheItem struct {
	ID       uint64 `json:"id"`
	FileName string `json:"file_name"`
	Source   string `json:"source"`
	Size     int    `json:"size"`
	MIMEType string `json:"mime_type"`
	CachedAt string `json:"cached_at"`
	Index    int    `json:"index"`
}

// TelegramStatusImageCacheSnapshot 描述当前进程内的状态图片缓存。
type TelegramStatusImageCacheSnapshot struct {
	Items    []TelegramStatusImageCacheItem `json:"items"`
	Total    int                            `json:"total"`
	Capacity int                            `json:"capacity"`
	Bytes    int                            `json:"bytes"`
}

// TelegramStatusImageCacheContent 是单张缓存图片的原始内容。
type TelegramStatusImageCacheContent struct {
	FileName string
	MIMEType string
	Binary   []byte
}

var ErrTelegramStatusImageCacheNotFound = errors.New("图片缓存不存在")

func nextTelegramStatusImageCacheIDLocked() uint64 {
	telegramStatusImageNextID++
	return telegramStatusImageNextID
}

func ListTelegramStatusImageCache() TelegramStatusImageCacheSnapshot {
	telegramStatusImageWindowMu.Lock()
	defer telegramStatusImageWindowMu.Unlock()

	items := make([]TelegramStatusImageCacheItem, 0, len(telegramStatusImageWindowItems))
	totalBytes := 0
	for index := range telegramStatusImageWindowItems {
		item := &telegramStatusImageWindowItems[index]
		ensureTelegramStatusImageCacheIDLocked(item)
		size := len(item.Binary)
		totalBytes += size
		items = append(items, TelegramStatusImageCacheItem{
			ID:       item.ID,
			FileName: item.FileName,
			Source:   item.Source,
			Size:     size,
			MIMEType: detectTelegramStatusImageMIME(item),
			CachedAt: formatTelegramStatusImageCachedAt(item.CachedAt),
			Index:    index + 1,
		})
	}

	return TelegramStatusImageCacheSnapshot{
		Items:    items,
		Total:    len(items),
		Capacity: telegramStatusImageWindowSize,
		Bytes:    totalBytes,
	}
}

func GetTelegramStatusImageCacheContent(id uint64) (TelegramStatusImageCacheContent, error) {
	if id == 0 {
		return TelegramStatusImageCacheContent{}, ErrTelegramStatusImageCacheNotFound
	}

	telegramStatusImageWindowMu.Lock()
	defer telegramStatusImageWindowMu.Unlock()

	for index := range telegramStatusImageWindowItems {
		item := &telegramStatusImageWindowItems[index]
		ensureTelegramStatusImageCacheIDLocked(item)
		if item.ID != id {
			continue
		}
		return TelegramStatusImageCacheContent{
			FileName: item.FileName,
			MIMEType: detectTelegramStatusImageMIME(item),
			Binary:   append([]byte(nil), item.Binary...),
		}, nil
	}
	return TelegramStatusImageCacheContent{}, ErrTelegramStatusImageCacheNotFound
}

func DeleteTelegramStatusImageCacheItem(id uint64) bool {
	if id == 0 {
		return false
	}

	telegramStatusImageWindowMu.Lock()
	for index := range telegramStatusImageWindowItems {
		item := &telegramStatusImageWindowItems[index]
		ensureTelegramStatusImageCacheIDLocked(item)
		if item.ID != id {
			continue
		}
		clearTelegramStatusBinary(item.Binary)
		telegramStatusImageWindowItems[index] = telegramStatusImageItem{}
		telegramStatusImageWindowItems = append(
			telegramStatusImageWindowItems[:index],
			telegramStatusImageWindowItems[index+1:]...,
		)
		telegramStatusImageWindowMu.Unlock()
		scheduleTelegramStatusImageRefill(RootContext())
		return true
	}
	telegramStatusImageWindowMu.Unlock()
	return false
}

func ensureTelegramStatusImageCacheIDLocked(item *telegramStatusImageItem) {
	if item == nil {
		return
	}
	if item.ID == 0 {
		item.ID = nextTelegramStatusImageCacheIDLocked()
	}
	if item.CachedAt.IsZero() {
		item.CachedAt = time.Now()
	}
}

func detectTelegramStatusImageMIME(item *telegramStatusImageItem) string {
	if item == nil {
		return "application/octet-stream"
	}
	if mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(item.FileName))); strings.HasPrefix(mimeType, "image/") {
		return mimeType
	}
	if len(item.Binary) > 0 {
		contentType := http.DetectContentType(item.Binary)
		if strings.HasPrefix(contentType, "image/") {
			return contentType
		}
	}
	if bytes.HasPrefix(item.Binary, []byte("GIF")) {
		return "image/gif"
	}
	return "application/octet-stream"
}

func formatTelegramStatusImageCachedAt(cachedAt time.Time) string {
	if cachedAt.IsZero() {
		return ""
	}
	return cachedAt.Format(time.RFC3339)
}
