package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestWaitForOpenAIVideoCompletionPollsUntilCompleted(t *testing.T) {
	oldPollInterval := videoTaskPollInterval
	oldPollTimeout := videoTaskPollTimeout
	videoTaskPollInterval = 10 * time.Millisecond
	videoTaskPollTimeout = 2 * time.Second
	t.Cleanup(func() {
		videoTaskPollInterval = oldPollInterval
		videoTaskPollTimeout = oldPollTimeout
	})

	var pollCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization 未透传，实际为 %q", got)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("轮询方法应为 GET，实际为 %s", r.Method)
		}
		if r.URL.Path != "/v1/videos/task_123" && r.URL.Path != "/v1/videos/task_123/status" {
			t.Fatalf("轮询路径不正确，实际为 %s", r.URL.Path)
		}

		current := atomic.AddInt32(&pollCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if current == 1 {
			_, _ = fmt.Fprint(w, `{"id":"task_123","task_id":"task_123","status":"queued","progress":20}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"task_123","task_id":"task_123","status":"completed","data":[{"url":"https://example.com/final.mp4"}]}`)
	}))
	defer server.Close()

	upstreamReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/videos", nil)
	if err != nil {
		t.Fatalf("构造上游请求失败: %v", err)
	}
	upstreamReq.Header.Set("Authorization", "Bearer test-token")

	initialBody := []byte(`{"id":"task_123","task_id":"task_123","status":"queued","progress":0}`)
	finalBody, err := waitForOpenAIVideoCompletion(context.Background(), upstreamReq, initialBody, "")
	if err != nil {
		t.Fatalf("视频轮询失败: %v", err)
	}
	if atomic.LoadInt32(&pollCount) < 2 {
		t.Fatalf("期望至少轮询 2 次，实际为 %d", atomic.LoadInt32(&pollCount))
	}
	if got := gjson.GetBytes(finalBody, "status").String(); got != "completed" {
		t.Fatalf("期望返回 completed，实际为 %s", got)
	}
	if got := gjson.GetBytes(finalBody, "data.0.url").String(); got != "https://example.com/final.mp4" {
		t.Fatalf("期望返回最终视频 URL，实际为 %s", got)
	}
}
