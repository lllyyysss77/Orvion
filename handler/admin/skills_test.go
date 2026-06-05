package admin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestLocateUploadedSkillSourceFromFolder(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("创建 Skill 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\n---\nDemo"), 0o644); err != nil {
		t.Fatalf("写入 Skill 文件失败: %v", err)
	}

	source, err := locateUploadedSkillSource(root)
	if err != nil {
		t.Fatalf("定位上传 Skill 失败: %v", err)
	}
	if source != skillDir {
		t.Fatalf("期望定位到 %s，实际为 %s", skillDir, source)
	}
}

func TestExtractUploadedSkillZipRejectsUnsafePath(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "unsafe.zip")
	file, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("创建 ZIP 文件失败: %v", err)
	}
	writer := zip.NewWriter(file)
	item, err := writer.Create("../SKILL.md")
	if err != nil {
		t.Fatalf("创建 ZIP 条目失败: %v", err)
	}
	if _, err := item.Write([]byte("unsafe")); err != nil {
		t.Fatalf("写入 ZIP 条目失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 ZIP writer 失败: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("关闭 ZIP 文件失败: %v", err)
	}

	if err := extractUploadedSkillZip(zipPath, filepath.Join(root, "out")); err == nil {
		t.Fatalf("包含不安全路径的 ZIP 应该被拒绝")
	}
}
