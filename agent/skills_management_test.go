package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/racio/orvion/models"
)

func TestTelegramAgentSkillFileManagement(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatalf("创建 Skill 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Demo Skill\n---\n说明"), 0o644); err != nil {
		t.Fatalf("写入 SKILL.md 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "notes.txt"), []byte("旧内容"), 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}

	cfg := models.TelegramAgentConfig{SkillsDir: root}
	tree, err := ListTelegramAgentSkillFilesForManagement(context.Background(), cfg, "demo")
	if err != nil {
		t.Fatalf("读取文件树失败: %v", err)
	}
	if tree.Skill.Name != "demo" || len(tree.Files) == 0 {
		t.Fatalf("文件树内容不正确: %+v", tree)
	}

	content, err := ReadTelegramAgentSkillFileForManagement(context.Background(), cfg, "demo", "notes.txt")
	if err != nil {
		t.Fatalf("读取文件内容失败: %v", err)
	}
	if !content.Editable || content.Content != "旧内容" {
		t.Fatalf("文件内容不正确: %+v", content)
	}

	updated, err := WriteTelegramAgentSkillFileForManagement(context.Background(), cfg, "demo", TelegramAgentSkillFileContentRequest{
		Path:    "notes.txt",
		Content: "新内容",
	})
	if err != nil {
		t.Fatalf("保存文件内容失败: %v", err)
	}
	if updated.Content != "新内容" {
		t.Fatalf("保存后的内容不正确: %+v", updated)
	}

	deleted, err := DeleteTelegramAgentSkillForManagement(context.Background(), cfg, "demo")
	if err != nil {
		t.Fatalf("删除 Skill 失败: %v", err)
	}
	if deleted.Name != "demo" {
		t.Fatalf("删除结果不正确: %+v", deleted)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("Skill 目录应已删除，实际错误: %v", err)
	}
}

func TestTelegramAgentSkillFilePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("创建 Skill 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\n---\n说明"), 0o644); err != nil {
		t.Fatalf("写入 SKILL.md 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("不应读取"), 0o644); err != nil {
		t.Fatalf("写入外部文件失败: %v", err)
	}

	cfg := models.TelegramAgentConfig{SkillsDir: root}
	if _, err := ReadTelegramAgentSkillFileForManagement(context.Background(), cfg, "demo", "../outside.txt"); err == nil {
		t.Fatalf("越权读取路径应该被拒绝")
	}
	if _, err := WriteTelegramAgentSkillFileForManagement(context.Background(), cfg, "demo", TelegramAgentSkillFileContentRequest{
		Path:    "../outside.txt",
		Content: "覆盖",
	}); err == nil {
		t.Fatalf("越权保存路径应该被拒绝")
	}
}
