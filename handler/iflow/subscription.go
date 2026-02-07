package iflow

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

type addIFlowCookieRequest struct {
	Cookie string `json:"cookie"`
}

func StartIFlowOAuth(c *gin.Context) {
	redirectURI := subscription.BuildIFlowOAuthRedirectURI(c.Request)
	result, err := subscription.GetIFlowSubscriptionManager().StartOAuthSession(redirectURI)
	if err != nil {
		common.InternalServerError(c, "创建 iFlow OAuth 会话失败: "+err.Error())
		return
	}
	common.Success(c, result)
}

func IFlowOAuthCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	authErr := c.Query("error")
	authErrDesc := c.Query("error_description")

	err := subscription.GetIFlowSubscriptionManager().PersistOAuthCallback(code, state, authErr, authErrDesc)
	if err != nil {
		c.Data(
			http.StatusOK,
			"text/html; charset=utf-8",
			[]byte(renderIFlowOAuthCallbackHTML("iFlow 授权回调处理失败", err.Error(), false)),
		)
		return
	}

	c.Data(
		http.StatusOK,
		"text/html; charset=utf-8",
		[]byte(renderIFlowOAuthCallbackHTML("iFlow 授权成功", "结果已写入系统，可返回管理页面查看。", true)),
	)
}

func GetIFlowOAuthStatus(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		common.BadRequest(c, "state 不能为空")
		return
	}

	status, err := subscription.GetIFlowSubscriptionManager().GetOAuthStatus(state)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrIFlowInvalidState), errors.Is(err, subscription.ErrIFlowSessionNotFound):
			common.BadRequest(c, err.Error())
		default:
			common.InternalServerError(c, "查询 iFlow OAuth 状态失败: "+err.Error())
		}
		return
	}

	common.Success(c, status)
}

func ListIFlowSubscriptions(c *gin.Context) {
	list, err := subscription.GetIFlowSubscriptionManager().ListSubscriptions()
	if err != nil {
		common.InternalServerError(c, "获取 iFlow 订阅列表失败: "+err.Error())
		return
	}
	common.Success(c, list)
}

func AddIFlowSubscriptionByCookie(c *gin.Context) {
	var req addIFlowCookieRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "cookie 不能为空")
		return
	}
	req.Cookie = strings.TrimSpace(req.Cookie)
	if req.Cookie == "" {
		common.BadRequest(c, "cookie 不能为空")
		return
	}

	sub, err := subscription.GetIFlowSubscriptionManager().AddSubscriptionByCookie(c.Request.Context(), req.Cookie)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrIFlowDuplicateBXAuth):
			common.ErrorWithHttpStatus(c, 409, 409, err.Error())
		default:
			common.BadRequest(c, "添加 iFlow 订阅失败: "+err.Error())
		}
		return
	}

	common.Success(c, sub)
}

func DeleteIFlowSubscription(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "id 不能为空")
		return
	}

	err := subscription.GetIFlowSubscriptionManager().DeleteSubscription(id)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrIFlowSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "删除 iFlow 订阅失败: "+err.Error())
		}
		return
	}

	common.SuccessWithMessage(c, "Deleted", gin.H{"id": id})
}

func GetIFlowSubscriptionModels(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		common.BadRequest(c, "id 不能为空")
		return
	}

	list, err := subscription.GetIFlowSubscriptionManager().ListAvailableModels(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, subscription.ErrIFlowSubscriptionNotFound):
			common.NotFound(c, err.Error())
		default:
			common.InternalServerError(c, "获取 iFlow 可用模型失败: "+err.Error())
		}
		return
	}

	common.Success(c, list)
}

func renderIFlowOAuthCallbackHTML(title, message string, success bool) string {
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
    <title>iFlow OAuth 回调</title>
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
            window.opener.postMessage({ type: "iflow-oauth-callback", success: %t }, "*");
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
