package codex

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/common"
	"github.com/racio/orvion/service/subscription"
)

func StartCodexSubscriptionOAuth(c *gin.Context) {
	redirectURI := subscription.BuildCodexOAuthRedirectURI(c.Request)
	result, err := subscription.GetCodexSubscriptionManager().StartOAuthSession(redirectURI)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrCodexOAuthNotConfigured):
			common.BadRequest(c, "Codex OAuth 未配置：请先设置 CODEX_OAUTH_CLIENT_ID")
		default:
			common.InternalServerError(c, "创建 Codex 授权会话失败: "+err.Error())
		}
		return
	}

	common.Success(c, result)
}

func CodexOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	authErr := c.Query("error")
	authErrDesc := c.Query("error_description")

	err := subscription.GetCodexSubscriptionManager().PersistCallback(code, state, authErr, authErrDesc)
	if err != nil {
		c.Data(
			http.StatusOK,
			"text/html; charset=utf-8",
			[]byte(renderCodexOAuthCallbackHTML("Codex 授权回调处理失败", err.Error(), false)),
		)
		return
	}

	c.Data(
		http.StatusOK,
		"text/html; charset=utf-8",
		[]byte(renderCodexOAuthCallbackHTML("Codex 授权成功", "结果已写入系统，可返回管理页面查看。", true)),
	)
}

func GetCodexSubscriptionOAuthStatus(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		common.BadRequest(c, "state 不能为空")
		return
	}

	status, err := subscription.GetCodexSubscriptionManager().GetOAuthStatus(state)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrCodexInvalidState), errors.Is(err, subscription.ErrCodexSessionNotFound):
			common.BadRequest(c, err.Error())
		default:
			common.InternalServerError(c, "查询授权状态失败: "+err.Error())
		}
		return
	}

	common.Success(c, status)
}

func ListCodexSubscriptions(c *gin.Context) {
	list, err := subscription.GetCodexSubscriptionManager().ListSubscriptions()
	if err != nil {
		common.InternalServerError(c, "获取 Codex 订阅列表失败: "+err.Error())
		return
	}
	common.Success(c, list)
}

func DeleteCodexSubscription(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "id 不能为空")
		return
	}

	err := subscription.GetCodexSubscriptionManager().DeleteSubscription(id)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrCodexSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "删除 Codex 订阅失败: "+err.Error())
		}
		return
	}
	common.SuccessWithMessage(c, "Deleted", gin.H{"id": id})
}

func GetCodexSubscriptionModels(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "id 不能为空")
		return
	}

	list, err := subscription.GetCodexSubscriptionManager().ListAvailableModels(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrCodexSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "获取 Codex 可用模型失败: "+err.Error())
		}
		return
	}

	common.Success(c, list)
}

func GetCodexSubscriptionTeamQuota(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "id 不能为空")
		return
	}

	quota, err := subscription.GetCodexSubscriptionManager().GetTeamQuota(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrCodexSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "查询 Codex team 额度失败: "+err.Error())
		}
		return
	}

	common.Success(c, quota)
}

func renderCodexOAuthCallbackHTML(title, message string, success bool) string {
	bgColor := "#ecfdf3"
	titleColor := "#065f46"
	if !success {
		bgColor = "#fef2f2"
		titleColor = "#991b1b"
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Codex OAuth 回调</title>
  </head>
  <body style="margin:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:%s;color:#111827;">
    <main style="max-width:560px;margin:8vh auto;padding:24px;">
      <section style="border:1px solid #e5e7eb;border-radius:16px;background:white;padding:20px;">
        <h1 style="font-size:20px;line-height:1.4;margin:0 0 12px;color:%s;">%s</h1>
        <p style="margin:0 0 16px;line-height:1.6;color:#374151;">%s</p>
        <p style="margin:0;padding:10px 12px;border-radius:10px;background:%s;font-size:13px;color:#4b5563;">
          你可以关闭当前窗口并返回管理页面。
        </p>
      </section>
    </main>
    <script>
      (function () {
        try {
          if (window.opener) {
            window.opener.postMessage({ type: "codex-oauth-callback", success: %t }, "*");
          }
        } catch (e) {}
        setTimeout(function () { window.close(); }, 1200);
      })();
    </script>
  </body>
</html>`,
		bgColor,
		titleColor,
		html.EscapeString(title),
		html.EscapeString(message),
		bgColor,
		success,
	)
}
