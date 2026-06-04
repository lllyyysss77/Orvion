package runtime

import (
	"net/http"
	"testing"
)

func TestBuildHeadersHandlesNilSourceWithForwarding(t *testing.T) {
	header := BuildHeaders(nil, true, map[string]string{"X-Custom": "ok"}, true)

	if header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("expected stream buffering header to be set")
	}
	if header.Get("X-Custom") != "ok" {
		t.Fatalf("expected custom header to be set")
	}
}

func TestBuildHeadersCopiesAndSanitizesSource(t *testing.T) {
	source := http.Header{
		"Authorization":     []string{"Bearer secret"},
		"X-Api-Key":         []string{"secret"},
		"X-Goog-Api-Key":    []string{"secret"},
		"X-Forwarded-Host":  []string{"example.com"},
		"X-Accel-Buffering": []string{"yes"},
	}

	header := BuildHeaders(source, true, map[string]string{"X-Forwarded-Host": "override.example"}, true)

	if header.Get("Authorization") != "" || header.Get("X-Api-Key") != "" || header.Get("X-Goog-Api-Key") != "" {
		t.Fatalf("expected sensitive auth headers to be removed")
	}
	if header.Get("X-Forwarded-Host") != "override.example" {
		t.Fatalf("expected custom header to override copied source header")
	}
	if header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("expected stream buffering header to be normalized")
	}
	if source.Get("X-Forwarded-Host") != "example.com" {
		t.Fatalf("expected source header to remain unchanged")
	}
}
