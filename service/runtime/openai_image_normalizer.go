package runtime

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// NormalizeOpenAIChatCompletionPayload 将 OpenAI Chat Completions 响应中的图片输出统一补齐为
// 非流式 choices[].message.images 与流式 choices[].delta.images 结构。
func NormalizeOpenAIChatCompletionPayload(payload []byte, stream bool) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}

	choices := gjson.GetBytes(payload, "choices")
	if !choices.Exists() || !choices.IsArray() {
		return payload
	}

	result := payload
	changed := false
	idx := 0
	choices.ForEach(func(_, choice gjson.Result) bool {
		var targetPath string
		var existing gjson.Result
		var urls []string

		if stream {
			targetPath = fmt.Sprintf("choices.%d.delta.images", idx)
			existing = choice.Get("delta.images")
			urls = append(urls, extractImageURLs(choice.Get("delta"))...)
			if len(urls) == 0 {
				urls = append(urls, extractImageURLs(choice.Get("message"))...)
			}
		} else {
			targetPath = fmt.Sprintf("choices.%d.message.images", idx)
			existing = choice.Get("message.images")
			urls = append(urls, extractImageURLs(choice.Get("message"))...)
			if len(urls) == 0 {
				urls = append(urls, extractImageURLs(choice.Get("delta"))...)
			}
		}

		urls = uniqueNonEmpty(urls)
		if len(urls) == 0 {
			idx++
			return true
		}
		if existing.Exists() && existing.IsArray() && len(existing.Array()) > 0 {
			idx++
			return true
		}

		imagesJSON := buildImagesJSONArray(urls)
		updated, err := sjson.SetRawBytes(result, targetPath, []byte(imagesJSON))
		if err == nil {
			result = updated
			changed = true
		}
		idx++
		return true
	})

	if !changed {
		return payload
	}
	return result
}

// NormalizeOpenAIStreamLine 处理单行 SSE 数据，若为 data: JSON 则尝试补齐 delta.images。
func NormalizeOpenAIStreamLine(line []byte) []byte {
	if len(line) == 0 {
		return line
	}

	trimmed, suffix := splitLineSuffix(line)
	if len(trimmed) == 0 || !bytes.HasPrefix(trimmed, []byte("data:")) {
		return line
	}

	payloadStart := len("data:")
	if len(trimmed) > payloadStart && trimmed[payloadStart] == ' ' {
		payloadStart++
	}
	if payloadStart >= len(trimmed) {
		return line
	}

	prefix := trimmed[:payloadStart]
	payload := bytes.TrimSpace(trimmed[payloadStart:])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return line
	}

	normalized := NormalizeOpenAIChatCompletionPayload(payload, true)
	if bytes.Equal(normalized, payload) {
		return line
	}

	out := make([]byte, 0, len(prefix)+len(normalized)+len(suffix))
	out = append(out, prefix...)
	if len(prefix) == len("data:") {
		out = append(out, ' ')
	}
	out = append(out, normalized...)
	out = append(out, suffix...)
	return out
}

// CopyStreamWithTransform 按行复制流数据并应用行转换函数（适用于 SSE）。
func CopyStreamWithTransform(src io.Reader, dst io.Writer, transform func([]byte) []byte) error {
	reader := bufio.NewReader(src)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if transform != nil {
				line = transform(line)
			}
			if _, writeErr := dst.Write(line); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func splitLineSuffix(line []byte) (trimmed []byte, suffix []byte) {
	end := len(line)
	for end > 0 {
		b := line[end-1]
		if b != '\n' && b != '\r' {
			break
		}
		end--
	}
	return line[:end], line[end:]
}

func extractImageURLs(node gjson.Result) []string {
	if !node.Exists() {
		return nil
	}

	urls := make([]string, 0, 2)

	// images 字段本身
	if images := node.Get("images"); images.Exists() {
		urls = append(urls, extractImageURLsFromAny(images)...)
	}

	// content 字段可能是数组化内容
	if content := node.Get("content"); content.Exists() {
		urls = append(urls, extractImageURLsFromAny(content)...)
	}

	// 兼容直接结构
	urls = append(urls, extractURLFromImageURLField(node.Get("image_url"))...)
	urls = append(urls, extractInlineDataURL(node)...) // inlineData / inline_data

	return urls
}

func extractImageURLsFromAny(value gjson.Result) []string {
	if !value.Exists() {
		return nil
	}

	if value.IsArray() {
		out := make([]string, 0, len(value.Array()))
		value.ForEach(func(_, item gjson.Result) bool {
			out = append(out, extractImageURLsFromAny(item)...)
			return true
		})
		return out
	}

	if value.Type == gjson.String {
		text := strings.TrimSpace(value.String())
		if isLikelyImageURL(text) {
			return []string{text}
		}
		return nil
	}

	if value.Type != gjson.JSON {
		return nil
	}

	out := make([]string, 0, 2)
	partType := strings.TrimSpace(value.Get("type").String())
	if partType == "image_url" || partType == "input_image" || partType == "output_image" || partType == "image" {
		out = append(out, extractURLFromImageURLField(value.Get("image_url"))...)
		if url := strings.TrimSpace(value.Get("url").String()); isLikelyImageURL(url) {
			out = append(out, url)
		}
	}

	out = append(out, extractURLFromImageURLField(value.Get("image_url"))...)
	out = append(out, extractInlineDataURL(value)...)
	if source := value.Get("source"); source.Exists() {
		out = append(out, extractInlineDataURL(source)...)
	}

	return out
}

func extractURLFromImageURLField(imageURL gjson.Result) []string {
	if !imageURL.Exists() {
		return nil
	}
	if imageURL.Type == gjson.String {
		url := strings.TrimSpace(imageURL.String())
		if isLikelyImageURL(url) {
			return []string{url}
		}
		return nil
	}

	url := strings.TrimSpace(imageURL.Get("url").String())
	if !isLikelyImageURL(url) {
		return nil
	}
	return []string{url}
}

func extractInlineDataURL(node gjson.Result) []string {
	inline := node.Get("inlineData")
	if !inline.Exists() {
		inline = node.Get("inline_data")
	}
	if !inline.Exists() {
		return nil
	}

	data := strings.TrimSpace(inline.Get("data").String())
	if data == "" {
		return nil
	}
	mime := strings.TrimSpace(inline.Get("mimeType").String())
	if mime == "" {
		mime = strings.TrimSpace(inline.Get("mime_type").String())
	}
	if mime == "" {
		mime = "image/png"
	}

	return []string{fmt.Sprintf("data:%s;base64,%s", mime, data)}
}

func isLikelyImageURL(url string) bool {
	url = strings.TrimSpace(url)
	if url == "" {
		return false
	}
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "data:image/") {
		return true
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		if strings.Contains(lower, ".png") || strings.Contains(lower, ".jpg") || strings.Contains(lower, ".jpeg") || strings.Contains(lower, ".webp") || strings.Contains(lower, ".gif") || strings.Contains(lower, ".bmp") || strings.Contains(lower, ".svg") {
			return true
		}
	}
	return false
}

func buildImagesJSONArray(urls []string) string {
	if len(urls) == 0 {
		return "[]"
	}
	items := make([]string, 0, len(urls))
	for _, u := range urls {
		escaped, _ := sjson.Set("{}", "url", u)
		item, _ := sjson.SetRaw("{}", "image_url", escaped)
		item, _ = sjson.Set(item, "type", "image_url")
		items = append(items, item)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func uniqueNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	// 保持输出稳定性，便于测试和日志对比
	sort.Strings(out)
	return out
}
