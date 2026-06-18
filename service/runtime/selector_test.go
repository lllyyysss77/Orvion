package runtime

import (
	"net/http"
	"testing"
)

func TestIsRetryableStatusOnlyRetriesServerErrors(t *testing.T) {
	nonRetryable := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusUnprocessableEntity,
	}
	for _, status := range nonRetryable {
		if IsRetryableStatus(status) {
			t.Fatalf("状态码 %d 不应重试", status)
		}
	}

	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if !IsRetryableStatus(status) {
			t.Fatalf("状态码 %d 应重试", status)
		}
	}
}
