package admin

import (
	"archive/zip"
	"errors"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/racio/orvion/agent"
	agenttools "github.com/racio/orvion/agent/tools"
	"github.com/racio/orvion/common"
)

type skillStatusRequest struct {
	Enabled bool `json:"enabled"`
}

const skillUploadMaxMemory = 128 << 20

// GetSkills 扫描并返回当前本地 Skills。
func GetSkills(c *gin.Context) {
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.ListTelegramAgentSkillsForManagement(
		c.Request.Context(),
		cfg,
		c.Query("query"),
	)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, result)
}

// GetSkill 读取指定 Skill 的完整说明。
func GetSkill(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		common.BadRequest(c, "Skill 名称不能为空")
		return
	}
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.ReadTelegramAgentSkillForManagement(c.Request.Context(), cfg, name)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

// GetSkillFiles 返回指定 Skill 目录下的文件树。
func GetSkillFiles(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		common.BadRequest(c, "Skill 名称不能为空")
		return
	}
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.ListTelegramAgentSkillFilesForManagement(c.Request.Context(), cfg, name)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

// GetSkillFileContent 读取指定 Skill 中的文本文件内容。
func GetSkillFileContent(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		common.BadRequest(c, "Skill 名称不能为空")
		return
	}
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.ReadTelegramAgentSkillFileForManagement(c.Request.Context(), cfg, name, c.Query("path"))
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

// UpdateSkillFileContent 保存指定 Skill 中的文本文件内容。
func UpdateSkillFileContent(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		common.BadRequest(c, "Skill 名称不能为空")
		return
	}
	var req agenttools.TelegramAgentSkillFileContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "请求参数错误: "+err.Error())
		return
	}
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.WriteTelegramAgentSkillFileForManagement(c.Request.Context(), cfg, name, req)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

// DeleteSkill 删除指定 Skill 目录。
func DeleteSkill(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		common.BadRequest(c, "Skill 名称不能为空")
		return
	}
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.DeleteTelegramAgentSkillForManagement(c.Request.Context(), cfg, name)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

// ReloadSkills 重新扫描本地 Skills，用于前端热重载按钮。
func ReloadSkills(c *gin.Context) {
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.ReloadTelegramAgentSkillsForManagement(
		c.Request.Context(),
		cfg,
		c.Query("query"),
	)
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	common.Success(c, result)
}

// UpdateSkillStatus 修改本地 Skill front matter 中的 enabled 状态。
func UpdateSkillStatus(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		common.BadRequest(c, "Skill 名称不能为空")
		return
	}
	var req skillStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.BadRequest(c, "请求参数错误: "+err.Error())
		return
	}
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	result, err := agenttools.SetTelegramAgentSkillEnabled(c.Request.Context(), cfg, name, req.Enabled)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

// UploadSkill 支持上传单个 Skill ZIP 压缩包。
func UploadSkill(c *gin.Context) {
	cfg, err := agent.LoadTelegramAgentConfig(c.Request.Context())
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	if err := c.Request.ParseMultipartForm(skillUploadMaxMemory); err != nil {
		common.BadRequest(c, "解析上传内容失败: "+err.Error())
		return
	}
	form := c.Request.MultipartForm
	if form == nil || len(form.File["files"]) == 0 {
		common.BadRequest(c, "请上传 Skill ZIP 压缩包")
		return
	}
	if len(form.File["files"]) != 1 || !strings.EqualFold(filepath.Ext(form.File["files"][0].Filename), ".zip") {
		common.BadRequest(c, "仅支持上传单个 ZIP 压缩包")
		return
	}

	tempDir, err := os.MkdirTemp("", "orvion-skill-upload-*")
	if err != nil {
		common.InternalServerError(c, err.Error())
		return
	}
	defer os.RemoveAll(tempDir)

	files := form.File["files"]
	var sourcePath string
	archivePath := filepath.Join(tempDir, "upload.zip")
	if err := saveUploadedSkillFile(files[0], archivePath); err != nil {
		common.BadRequest(c, "保存 ZIP 失败: "+err.Error())
		return
	}
	extractDir := filepath.Join(tempDir, "extract")
	if err := extractUploadedSkillZip(archivePath, extractDir); err != nil {
		common.BadRequest(c, "解压 ZIP 失败: "+err.Error())
		return
	}
	sourcePath, err = locateUploadedSkillSource(extractDir)
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}

	result, err := agenttools.ImportTelegramAgentSkill(c.Request.Context(), cfg, agenttools.TelegramAgentSkillImportRequest{
		SourcePath: sourcePath,
		Name:       strings.TrimSpace(c.PostForm("name")),
		Overwrite:  parseSkillUploadBool(c.PostForm("overwrite")),
	})
	if err != nil {
		common.BadRequest(c, err.Error())
		return
	}
	common.Success(c, result)
}

func saveUploadedSkillFile(file *multipart.FileHeader, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := file.Open()
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func extractUploadedSkillZip(zipPath string, targetRoot string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, item := range reader.File {
		if strings.HasPrefix(item.Name, "__MACOSX/") {
			continue
		}
		target, err := safeUploadedSkillPath(targetRoot, item.Name)
		if err != nil {
			return err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := item.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, item.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func locateUploadedSkillSource(root string) (string, error) {
	root = filepath.Clean(root)
	if hasUploadedSkillFile(root) {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	candidates := make([]string, 0, 1)
	fileCandidates := make([]string, 0, 1)
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			if hasUploadedSkillFile(path) {
				candidates = append(candidates, path)
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			fileCandidates = append(fileCandidates, path)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return "", errors.New("上传内容包含多个 Skill，请一次只上传一个")
	}
	if len(fileCandidates) == 1 {
		return fileCandidates[0], nil
	}
	return "", errors.New("上传内容中未找到 skills.md 或 SKILL.md")
}

func hasUploadedSkillFile(dir string) bool {
	for _, name := range []string{"skills.md", "SKILL.md"} {
		path := filepath.Join(dir, name)
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			return true
		}
	}
	return false
}

func safeUploadedSkillPath(root string, rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" {
		return "", errors.New("上传文件路径为空")
	}
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) || cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", errors.New("上传文件路径不安全：" + rel)
	}
	return filepath.Join(root, cleaned), nil
}

func parseSkillUploadBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
