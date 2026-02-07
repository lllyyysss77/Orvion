package runtime

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const MaxLogBodyBytes = 8 * 1024

func SafeBodyTextForLog(res *http.Response, body []byte) string {
	if len(body) == 0 {
		return ""
	}

	decoded := body
	decodedLabel := ""

	contentEncoding := strings.ToLower(strings.TrimSpace(res.Header.Get("Content-Encoding")))
	isGzip := contentEncoding == "gzip" || (len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b)
	if isGzip {
		if zr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
			if b, err := io.ReadAll(zr); err == nil {
				decoded = b
				decodedLabel = " (gzip 解压后)"
			}
			_ = zr.Close()
		}
	}

	const maxBytes = 4096
	truncated := false
	totalBytes := len(decoded)
	if totalBytes > maxBytes {
		decoded = decoded[:maxBytes]
		truncated = true
	}

	if utf8.Valid(decoded) {
		text := string(decoded)
		if truncated {
			return fmt.Sprintf("%s%s...(已截断，总计 %d 字节)", text, decodedLabel, totalBytes)
		}
		return text + decodedLabel
	}

	b64 := base64.StdEncoding.EncodeToString(decoded)
	if truncated {
		return fmt.Sprintf("base64%s:%s...(已截断，总计 %d 字节)", decodedLabel, b64, totalBytes)
	}
	return fmt.Sprintf("base64%s:%s", decodedLabel, b64)
}
