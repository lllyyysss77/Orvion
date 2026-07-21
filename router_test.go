package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCacheWebUIAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	assets := router.Group("/assets")
	assets.Use(cacheWebUIAssets)
	assets.GET("/:name", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/assets/app-hash.js", nil)
	router.ServeHTTP(response, request)

	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control=%q", got)
	}
}
