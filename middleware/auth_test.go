package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/consts"
)

func TestAuthOpenAIOptionalAllowsMissingKey(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/v1/models", AuthOpenAIOptional("admin-token"), func(c *gin.Context) {
		allowAll, _ := c.Request.Context().Value(consts.ContextKeyAllowAllModel).(bool)
		if !allowAll {
			t.Fatalf("未携带 key 时应放行并标记允许全部模型")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("状态码异常: got=%d want=%d", rec.Code, http.StatusNoContent)
	}
}

func TestAuthOpenAIStillRejectsMissingKey(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/v1/chat/completions", AuthOpenAI("admin-token"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码异常: got=%d want=%d", rec.Code, http.StatusUnauthorized)
	}
}
