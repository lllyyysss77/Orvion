package runtime

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxResponsePreviewBytes = 2 * 1024

// ValidateSuccessfulResponseBody 校验 200 响应体是否“有效成功”。
// 为避免吞掉后续读取，这里会回放已读取的前缀数据。
func ValidateSuccessfulResponseBody(res *http.Response, stream bool) error {
	if res == nil || res.Body == nil {
		return errors.New("upstream returned empty response body")
	}

	originBody := res.Body
	buffered := bufio.NewReaderSize(originBody, maxResponsePreviewBytes)
	preview, readErr := buffered.Peek(maxResponsePreviewBytes)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, bufio.ErrBufferFull) {
		return fmt.Errorf("peek upstream response body failed: %w", readErr)
	}

	if len(preview) == 0 && errors.Is(readErr, io.EOF) {
		return errors.New("upstream returned empty response body")
	}

	if errors.Is(readErr, io.EOF) {
		trimmed := bytes.TrimSpace(preview)
		if len(trimmed) == 0 {
			return errors.New("upstream returned blank response body")
		}
		if stream && isDoneOnlyStreamPayload(trimmed) {
			return errors.New("upstream returned done-only stream payload")
		}
	}

	res.Body = struct {
		io.Reader
		io.Closer
	}{
		Reader: buffered,
		Closer: originBody,
	}
	return nil
}

func isDoneOnlyStreamPayload(payload []byte) bool {
	lines := bytes.Split(payload, []byte{'\n'})
	meaningful := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.ToLower(strings.TrimSpace(string(line)))
		if text == "" {
			continue
		}
		meaningful = append(meaningful, text)
	}
	if len(meaningful) != 1 {
		return false
	}
	return meaningful[0] == "data: [done]" || meaningful[0] == "[done]"
}
