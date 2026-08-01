package service

import (
	"testing"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
)

func TestEstimateUsageFromIORawOutputFallback(t *testing.T) {
	output := &models.OutputUnion{
		OfString: `{"completed_at":1781689211,"status":"completed","result_id":"job_123"}`,
	}

	usage := estimateUsageFromIO(consts.StyleOpenAI, "custom-batch-v1", []byte(`{"messages":[{"role":"user","content":"处理这段内容"}]}`), output)
	if usage.CompletionTokens == 0 {
		t.Fatalf("completion tokens should be estimated from raw output")
	}
	if usage.TotalTokens == 0 {
		t.Fatalf("total tokens should be estimated")
	}
}
