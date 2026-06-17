package handler

import (
	"strings"
	"testing"

	"github.com/racio/orvion/consts"
)

func TestExtractChatContentVideoCompletedResponse(t *testing.T) {
	raw := []byte(`{
  "completed_at": 1781689211,
  "created_at": 1781689088,
  "error": null,
  "expires_at": null,
  "id": "task_w5to6JmdFzhriOQ5gJCjIfNiXoOVjfUl",
  "model": "agnes-video-v2.0",
  "object": "video",
  "progress": 100,
  "remixed_from_video_id": "https://platform-outputs.agnes-ai.space/videos/agnes-video-v2.0/2026/06/17/video_675593afedab30ab9a60f55e0abc2e5b29dc72001f37c54f.mp4",
  "seconds": "5.0",
  "size": "1280x704",
  "started_at": 1781689088,
  "status": "completed",
  "video_id": "video_675593afedab30ab9a60f55e0abc2e5b29dc72001f37c54f"
}`)

	got := extractChatContent(consts.StyleOpenAI, "videos", raw)
	if got == "" {
		t.Fatalf("视频完成态不应解析为空")
	}
	if !strings.Contains(got, "platform-outputs.agnes-ai.space") {
		t.Fatalf("视频完成态应优先解析出可访问 URL，实际为: %s", got)
	}
}
