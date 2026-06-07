package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	telegramAgentGeneratedAttachmentDirName  = "orvion-agent-attachments"
	telegramAgentGeneratedAttachmentMaxBytes = 1024 * 1024
	telegramAgentGeneratedAttachmentNameMax  = 80
)

var (
	telegramAgentAttachmentFileNameUnsafePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	telegramAgentAttachmentAllowedTextExts       = map[string]struct{}{
		".css":  {},
		".csv":  {},
		".htm":  {},
		".html": {},
		".js":   {},
		".json": {},
		".log":  {},
		".md":   {},
		".svg":  {},
		".text": {},
		".ts":   {},
		".txt":  {},
		".xml":  {},
		".yaml": {},
		".yml":  {},
	}
)

func createTelegramAgentAttachmentFile(args telegramAgentToolCallArgs) (string, error) {
	content := args.Content
	if strings.TrimSpace(content) == "" {
		return "", errors.New("文件内容不能为空")
	}
	contentBytes := []byte(content)
	if len(contentBytes) > telegramAgentGeneratedAttachmentMaxBytes {
		return "", fmt.Errorf("文件内容超过大小限制: %d > %d", len(contentBytes), telegramAgentGeneratedAttachmentMaxBytes)
	}

	fileName, err := sanitizeTelegramAgentAttachmentFileName(args.FileName)
	if err != nil {
		return "", err
	}

	dir := filepath.Join(os.TempDir(), telegramAgentGeneratedAttachmentDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建附件目录失败: %w", err)
	}
	path := filepath.Join(dir, uniqueTelegramAgentAttachmentFileName(fileName))
	if err := os.WriteFile(path, contentBytes, 0o644); err != nil {
		return "", fmt.Errorf("写入附件文件失败: %w", err)
	}

	kind, err := normalizeTelegramAgentAttachmentKind(args.AttachmentKind, fileName)
	if err != nil {
		return "", err
	}
	caption := strings.TrimSpace(args.Caption)
	if caption == "" {
		caption = fileName
	}
	caption = limitTelegramAgentAttachmentCaption(caption)

	return strings.Join([]string{
		"已生成附件文件",
		"文件名：" + fileName,
		fmt.Sprintf("[orvion:%s:%s|%s]", kind, path, caption),
	}, "\n"), nil
}

func sanitizeTelegramAgentAttachmentFileName(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("文件名不能为空")
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	base := strings.TrimSpace(filepath.Base(raw))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", errors.New("文件名无效")
	}

	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" {
		ext = ".txt"
	}
	if _, ok := telegramAgentAttachmentAllowedTextExts[ext]; !ok {
		return "", fmt.Errorf("不支持的附件文件类型: %s", ext)
	}

	name := strings.TrimSuffix(base, filepath.Ext(base))
	name = telegramAgentAttachmentFileNameUnsafePattern.ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-_")
	if name == "" {
		name = "attachment"
	}
	if len(name) > telegramAgentGeneratedAttachmentNameMax {
		name = name[:telegramAgentGeneratedAttachmentNameMax]
	}
	return name + ext, nil
}

func uniqueTelegramAgentAttachmentFileName(fileName string) string {
	return time.Now().Format("20060102-150405") + "-" + telegramAgentAttachmentRandomSuffix() + "-" + fileName
}

func telegramAgentAttachmentRandomSuffix() string {
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func normalizeTelegramAgentAttachmentKind(raw string, fileName string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(raw))
	if kind == "" {
		kind = telegramAgentAttachmentKindFile
	}
	if filepath.Ext(fileName) == ".svg" {
		return telegramAgentAttachmentKindFile, nil
	}
	switch kind {
	case telegramAgentAttachmentKindFile, telegramAgentAttachmentKindImage:
		return kind, nil
	default:
		return "", fmt.Errorf("附件类型无效: %s", raw)
	}
}
