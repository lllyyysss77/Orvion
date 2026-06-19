package main

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/racio/orvion/models"
)

func TestSanitizeBatchUTF8ReplacesInvalidStrings(t *testing.T) {
	rows := []models.ChatIO{
		{
			ID:           36,
			LogUUID:      "valid-uuid",
			OutputString: string([]byte{'o', 'k', 0xc4, '(', 0x9d, 'x'}),
		},
	}

	cleaned := sanitizeBatchUTF8(&rows)
	if cleaned != 1 {
		t.Fatalf("cleaned = %d, want 1", cleaned)
	}
	if !utf8.ValidString(rows[0].OutputString) {
		t.Fatalf("output_string should be valid utf8 after sanitize")
	}
	if rows[0].OutputString != "ok\uFFFD(\uFFFDx" {
		t.Fatalf("unexpected sanitized value: %q", rows[0].OutputString)
	}
}

func TestSanitizeBatchUTF8HandlesChatLogStruct(t *testing.T) {
	rows := []models.ChatLog{
		{
			CreatedAt:    time.Now(),
			Name:         string([]byte{'g', 'p', 't', 0xff}),
			ProviderName: "provider",
			Status:       "success",
			Usage:        models.Usage{PromptTokensDetails: string([]byte{'{', 0xfe, '}'}), CompletionTokens: 1},
		},
	}

	cleaned := sanitizeBatchUTF8(&rows)
	if cleaned != 2 {
		t.Fatalf("cleaned = %d, want 2", cleaned)
	}
	if !utf8.ValidString(rows[0].Name) || !utf8.ValidString(rows[0].PromptTokensDetails) {
		t.Fatalf("chat log strings should be valid utf8 after sanitize")
	}
}
