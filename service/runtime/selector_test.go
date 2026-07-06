package runtime

import (
	"net/http"
	"testing"
)

func TestIsRetryableStatusRetries429AndServerErrors(t *testing.T) {
	nonRetryable := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusRequestTimeout,
		http.StatusUnprocessableEntity,
	}
	for _, status := range nonRetryable {
		if IsRetryableStatus(status) {
			t.Fatalf("状态码 %d 不应重试", status)
		}
	}

	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if !IsRetryableStatus(status) {
			t.Fatalf("状态码 %d 应重试", status)
		}
	}
}

func TestIsFallbackStatusMatchesConfiguredClientErrors(t *testing.T) {
	fallbackStatuses := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusUnprocessableEntity,
	}
	for _, status := range fallbackStatuses {
		if !IsFallbackStatus(status) {
			t.Fatalf("状态码 %d 应触发模型回退", status)
		}
		if IsRetryableStatus(status) {
			t.Fatalf("状态码 %d 应触发模型回退但不应重试当前模型", status)
		}
	}

	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if IsFallbackStatus(status) {
			t.Fatalf("状态码 %d 不应走指定 4xx 回退分支", status)
		}
	}
}
