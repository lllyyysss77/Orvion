package service

import (
	"testing"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
)

func TestEstimateUsageFromIORawOutputFallback(t *testing.T) {
	output := &models.OutputUnion{
		OfString: `{"completed_at":1781689211,"status":"completed","video_id":"video_123"}`,
	}

	usage := estimateUsageFromIO(consts.StyleOpenAI, "agnes-video-v2.0", []byte(`{"messages":[{"role":"user","content":"生成一个视频"}]}`), output)
	if usage.CompletionTokens == 0 {
		t.Fatalf("completion tokens should be estimated from raw output")
	}
	if usage.TotalTokens == 0 {
		t.Fatalf("total tokens should be estimated")
	}
}
