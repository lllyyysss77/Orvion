package runtime

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidateSuccessfulResponseBody_EmptyBody(t *testing.T) {
	res := &http.Response{Body: io.NopCloser(strings.NewReader(""))}
	err := ValidateSuccessfulResponseBody(res, false)
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
}

func TestValidateSuccessfulResponseBody_BlankBody(t *testing.T) {
	res := &http.Response{Body: io.NopCloser(strings.NewReader(" \n\t "))}
	err := ValidateSuccessfulResponseBody(res, false)
	if err == nil {
		t.Fatal("expected error for blank body, got nil")
	}
}

func TestValidateSuccessfulResponseBody_DoneOnlyStream(t *testing.T) {
	res := &http.Response{Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}
	err := ValidateSuccessfulResponseBody(res, true)
	if err == nil {
		t.Fatal("expected error for done-only stream body, got nil")
	}
}

func TestValidateSuccessfulResponseBody_KeepReadableBody(t *testing.T) {
	raw := `{"id":"ok","choices":[{"message":{"content":"hello"}}]}`
	res := &http.Response{Body: io.NopCloser(strings.NewReader(raw))}
	if err := ValidateSuccessfulResponseBody(res, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	if string(got) != raw {
		t.Fatalf("body mismatch, got=%q want=%q", string(got), raw)
	}
}
