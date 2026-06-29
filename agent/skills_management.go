package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/racio/orvion/models"
	"gorm.io/gorm"
)

const (
	telegramAgentSkillFileMaxBytes = 1024 * 1024
	telegramAgentSkillMaxFileNodes = 2000
)

type TelegramAgentSkillScriptView struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Description string   `json:"description"`
	TimeoutMs   int      `json:"timeout_ms"`
	Usage       []string `json:"usage,omitempty"`
}

type TelegramAgentSkillView struct {
	Name         string                         `json:"name"`
	Description  string                         `json:"description"`
	Enabled      bool                           `json:"enabled"`
	Dir          string                         `json:"dir"`
	File         string                         `json:"file"`
	Instructions string                         `json:"instructions"`
	Triggers     []string                       `json:"triggers"`
	Scripts      []TelegramAgentSkillScriptView `json:"scripts"`
}

type TelegramAgentSkillListResult struct {
	Skills        []TelegramAgentSkillView `json:"skills"`
	Total         int                      `json:"total"`
	SkillsEnabled bool                     `json:"skills_enabled"`
	Query         string                   `json:"query"`
	ScannedAt     time.Time                `json:"scanned_at"`
}

type TelegramAgentSkillReloadResult struct {
	TelegramAgentSkillListResult
	Message string `json:"message"`
}

type TelegramAgentSkillImportRequest struct {
	SourcePath string `json:"source_path"`
	Name       string `json:"name"`
	Overwrite  bool   `json:"overwrite"`
}

type TelegramAgentSkillFileNode struct {
	Name       string                       `json:"name"`
	Path       string                       `json:"path"`
	Kind       string                       `json:"kind"`
	Size       int64                        `json:"size"`
	ModifiedAt time.Time                    `json:"modified_at"`
	Children   []TelegramAgentSkillFileNode `json:"children"`
}

type TelegramAgentSkillFileTreeResult struct {
	Skill TelegramAgentSkillView       `json:"skill"`
	Root  string                       `json:"root"`
	Files []TelegramAgentSkillFileNode `json:"files"`
}

type TelegramAgentSkillFileContentResult struct {
	Skill      string    `json:"skill"`
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	Editable   bool      `json:"editable"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type TelegramAgentSkillFileContentRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type TelegramAgentSkillDeleteResult struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Message string `json:"message"`
}

func ListTelegramAgentSkillsForManagement(ctx context.Context, cfg models.TelegramAgentConfig, query string) (TelegramAgentSkillListResult, error) {
	skills, err := scanTelegramAgentSkills(ctx, cfg)
	if err != nil {
		return TelegramAgentSkillListResult{}, err
	}
	filtered, err := filterTelegramAgentSkillViews(skills, query)
	if err != nil {
		return TelegramAgentSkillListResult{}, err
	}
	return TelegramAgentSkillListResult{
		Skills:        filtered,
		Total:         len(filtered),
		SkillsEnabled: telegramAgentSkillsEnabled(cfg),
		Query:         strings.TrimSpace(query),
		ScannedAt:     time.Now(),
	}, nil
}

func ReloadTelegramAgentSkillsForManagement(ctx context.Context, cfg models.TelegramAgentConfig, query string) (TelegramAgentSkillReloadResult, error) {
	result, err := ListTelegramAgentSkillsForManagement(ctx, cfg, query)
	if err != nil {
		return TelegramAgentSkillReloadResult{}, err
	}
	return TelegramAgentSkillReloadResult{
		TelegramAgentSkillListResult: result,
		Message:                      fmt.Sprintf("已重新扫描本地 Skills，共 %d 个", result.Total),
	}, nil
}

func syncTelegramAgentSkills(ctx context.Context, root string, scanned []telegramAgentSkill) ([]telegramAgentSkill, error) {
	if models.DB == nil {
		return scanned, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	activeNames := make(map[string]struct{}, len(scanned))
	result := make([]telegramAgentSkill, 0, len(scanned))
	for _, skill := range scanned {
		if strings.TrimSpace(skill.Name) == "" {
			continue
		}
		activeNames[skill.Name] = struct{}{}
		record, err := upsertTelegramAgentSkillRecord(ctx, skill)
		if err != nil {
			return nil, err
		}
		skill.Enabled = record.Enabled != 0
		result = append(result, skill)
	}
	if err := cleanupMissingTelegramAgentSkillRecords(ctx, root, activeNames); err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func upsertTelegramAgentSkillRecord(ctx context.Context, skill telegramAgentSkill) (models.TelegramAgentSkill, error) {
	now := time.Now()
	triggersJSON, err := json.Marshal(skill.Triggers)
	if err != nil {
		return models.TelegramAgentSkill{}, err
	}
	scriptsJSON, err := json.Marshal(telegramAgentSkillScriptViews(skill.Scripts))
	if err != nil {
		return models.TelegramAgentSkill{}, err
	}

	var record models.TelegramAgentSkill
	err = models.DB.WithContext(ctx).Where("name = ?", skill.Name).First(&record).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.TelegramAgentSkill{}, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		record = models.TelegramAgentSkill{
			Name:    skill.Name,
			Enabled: 1,
		}
	}

	record.Dir = filepath.Clean(skill.Dir)
	record.File = filepath.Clean(skill.File)
	record.Description = skill.Description
	record.Instructions = skill.Instructions
	record.Triggers = string(triggersJSON)
	record.Scripts = string(scriptsJSON)
	record.SearchText = telegramAgentSkillSearchText(skill)
	record.ScannedAt = now

	if record.ID == 0 {
		if err := models.DB.WithContext(ctx).Create(&record).Error; err != nil {
			return models.TelegramAgentSkill{}, err
		}
		return record, nil
	}
	if err := models.DB.WithContext(ctx).Save(&record).Error; err != nil {
		return models.TelegramAgentSkill{}, err
	}
	return record, nil
}

func cleanupMissingTelegramAgentSkillRecords(ctx context.Context, root string, activeNames map[string]struct{}) error {
	var records []models.TelegramAgentSkill
	if err := models.DB.WithContext(ctx).Find(&records).Error; err != nil {
		return err
	}
	for _, record := range records {
		if !telegramAgentSkillRecordInsideRoot(root, record) {
			continue
		}
		if _, ok := activeNames[record.Name]; ok {
			continue
		}
		if err := models.DB.WithContext(ctx).Delete(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

func telegramAgentSkillRecordInsideRoot(root string, record models.TelegramAgentSkill) bool {
	dir := strings.TrimSpace(record.Dir)
	if dir == "" {
		return false
	}
	return ensureTelegramSkillPathInside(root, dir) == nil
}

func telegramAgentSkillScriptViews(scripts []telegramAgentSkillScript) []TelegramAgentSkillScriptView {
	result := make([]TelegramAgentSkillScriptView, 0, len(scripts))
	for _, script := range scripts {
		result = append(result, TelegramAgentSkillScriptView{
			Name:        script.Name,
			Path:        filepath.ToSlash(script.Path),
			Description: script.Description,
			TimeoutMs:   script.TimeoutMs,
			Usage:       append([]string{}, script.Usage...),
		})
	}
	return result
}

func loadTelegramAgentSkillsFromDatabase(ctx context.Context, cfg models.TelegramAgentConfig, enabledOnly bool) ([]telegramAgentSkill, error) {
	records, err := loadTelegramAgentSkillRecords(ctx, cfg, enabledOnly)
	if err != nil {
		return nil, err
	}
	skills := make([]telegramAgentSkill, 0, len(records))
	for _, record := range records {
		skills = append(skills, telegramAgentSkillFromRecord(record))
	}
	return skills, nil
}

func loadTelegramAgentSkillRecords(ctx context.Context, cfg models.TelegramAgentConfig, enabledOnly bool) ([]models.TelegramAgentSkill, error) {
	if models.DB == nil {
		return nil, nil
	}
	root, err := resolveTelegramAgentSkillsRoot(cfg)
	if err != nil {
		return nil, err
	}
	query := models.DB.WithContext(ctx).Order("LOWER(name) ASC")
	if enabledOnly {
		query = query.Where("enabled = ?", 1)
	}
	var records []models.TelegramAgentSkill
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}
	filtered := make([]models.TelegramAgentSkill, 0, len(records))
	for _, record := range records {
		if telegramAgentSkillRecordInsideRoot(root, record) {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}

func telegramAgentSkillFromRecord(record models.TelegramAgentSkill) telegramAgentSkill {
	var triggers []string
	_ = json.Unmarshal([]byte(record.Triggers), &triggers)
	var scriptViews []TelegramAgentSkillScriptView
	_ = json.Unmarshal([]byte(record.Scripts), &scriptViews)

	skill := telegramAgentSkill{
		Name:         record.Name,
		Description:  record.Description,
		Enabled:      record.Enabled != 0,
		Dir:          filepath.Clean(record.Dir),
		File:         filepath.Clean(record.File),
		Instructions: record.Instructions,
		Triggers:     orderedUniqueStrings(triggers),
	}
	scripts := make([]telegramAgentSkillScript, 0, len(scriptViews))
	for _, script := range scriptViews {
		scripts = append(scripts, telegramAgentSkillScript{
			Name:        script.Name,
			Path:        script.Path,
			Description: script.Description,
			TimeoutMs:   script.TimeoutMs,
			Usage:       append([]string{}, script.Usage...),
		})
	}
	skill.Scripts = normalizeTelegramSkillScripts(skill, scripts)
	return skill
}

func loadTelegramAgentEnabledSkills(ctx context.Context, cfg models.TelegramAgentConfig) ([]telegramAgentSkill, error) {
	scanned, err := scanTelegramAgentSkills(ctx, cfg)
	if err != nil {
		return nil, err
	}
	skills := filterEnabledTelegramAgentSkills(scanned)
	if models.DB != nil {
		skills, err = loadTelegramAgentSkillsFromDatabase(ctx, cfg, true)
		if err != nil {
			return nil, err
		}
	}
	if len(skills) == 0 {
		return []telegramAgentSkill{}, nil
	}
	return skills, nil
}

func filterEnabledTelegramAgentSkills(skills []telegramAgentSkill) []telegramAgentSkill {
	result := make([]telegramAgentSkill, 0, len(skills))
	for _, skill := range skills {
		if skill.Enabled {
			result = append(result, skill)
		}
	}
	return result
}

func ReadTelegramAgentSkillForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string) (TelegramAgentSkillView, error) {
	skill, err := findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	return toTelegramAgentSkillView(skill), nil
}

func ListTelegramAgentSkillFilesForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string) (TelegramAgentSkillFileTreeResult, error) {
	skill, err := findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillFileTreeResult{}, err
	}
	count := 0
	files, err := buildTelegramAgentSkillFileNodes(skill.Dir, "", &count)
	if err != nil {
		return TelegramAgentSkillFileTreeResult{}, err
	}
	return TelegramAgentSkillFileTreeResult{
		Skill: toTelegramAgentSkillView(skill),
		Root:  skill.Dir,
		Files: files,
	}, nil
}

func ReadTelegramAgentSkillFileForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string, relPath string) (TelegramAgentSkillFileContentResult, error) {
	skill, err := findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	target, cleanRel, err := resolveTelegramAgentSkillFilePath(skill, relPath)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	stat, err := os.Stat(target)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	if stat.IsDir() {
		return TelegramAgentSkillFileContentResult{}, errors.New("只能读取文件，不能读取目录")
	}
	if stat.Size() > telegramAgentSkillFileMaxBytes {
		return TelegramAgentSkillFileContentResult{}, fmt.Errorf("文件超过 %d KB，暂不支持在线编辑", telegramAgentSkillFileMaxBytes/1024)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	result := TelegramAgentSkillFileContentResult{
		Skill:      skill.Name,
		Path:       cleanRel,
		Editable:   isTelegramAgentSkillTextFile(raw),
		Size:       stat.Size(),
		ModifiedAt: stat.ModTime(),
	}
	if result.Editable {
		result.Content = string(raw)
	}
	return result, nil
}

func WriteTelegramAgentSkillFileForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string, req TelegramAgentSkillFileContentRequest) (TelegramAgentSkillFileContentResult, error) {
	skill, err := findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	target, cleanRel, err := resolveTelegramAgentSkillFilePath(skill, req.Path)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	raw := []byte(req.Content)
	if len(raw) > telegramAgentSkillFileMaxBytes {
		return TelegramAgentSkillFileContentResult{}, fmt.Errorf("文件内容超过 %d KB，暂不支持保存", telegramAgentSkillFileMaxBytes/1024)
	}
	if !isTelegramAgentSkillTextFile(raw) {
		return TelegramAgentSkillFileContentResult{}, errors.New("只能保存 UTF-8 文本文件")
	}
	stat, err := os.Stat(target)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	if stat.IsDir() {
		return TelegramAgentSkillFileContentResult{}, errors.New("只能保存文件，不能保存目录")
	}
	if err := os.WriteFile(target, raw, stat.Mode().Perm()); err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	nextStat, err := os.Stat(target)
	if err != nil {
		return TelegramAgentSkillFileContentResult{}, err
	}
	return TelegramAgentSkillFileContentResult{
		Skill:      skill.Name,
		Path:       cleanRel,
		Content:    req.Content,
		Editable:   true,
		Size:       nextStat.Size(),
		ModifiedAt: nextStat.ModTime(),
	}, nil
}

func DeleteTelegramAgentSkillForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string) (TelegramAgentSkillDeleteResult, error) {
	root, err := resolveTelegramAgentSkillsRoot(cfg)
	if err != nil {
		return TelegramAgentSkillDeleteResult{}, err
	}
	skill, err := findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillDeleteResult{}, err
	}
	if err := ensureTelegramSkillPathInside(root, skill.Dir); err != nil {
		return TelegramAgentSkillDeleteResult{}, err
	}
	if same, err := sameTelegramAgentSkillPath(root, skill.Dir); err != nil {
		return TelegramAgentSkillDeleteResult{}, err
	} else if same {
		return TelegramAgentSkillDeleteResult{}, errors.New("不能删除 Skills 根目录本身")
	}
	if !hasTelegramSkillFile(skill.Dir) {
		return TelegramAgentSkillDeleteResult{}, errors.New("目标目录不是有效 Skill，已拒绝删除")
	}
	if err := os.RemoveAll(skill.Dir); err != nil {
		return TelegramAgentSkillDeleteResult{}, err
	}
	if models.DB != nil {
		if err := models.DB.WithContext(ctx).Where("name = ?", skill.Name).Delete(&models.TelegramAgentSkill{}).Error; err != nil {
			return TelegramAgentSkillDeleteResult{}, err
		}
	}
	return TelegramAgentSkillDeleteResult{
		Name:    skill.Name,
		Dir:     skill.Dir,
		Message: fmt.Sprintf("已移除 Skill：%s", skill.Name),
	}, nil
}

func SetTelegramAgentSkillEnabled(ctx context.Context, cfg models.TelegramAgentConfig, name string, enabled bool) (TelegramAgentSkillView, error) {
	skill, err := findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	if models.DB == nil {
		skill.Enabled = enabled
		return toTelegramAgentSkillView(skill), nil
	}
	if err := models.DB.WithContext(ctx).Model(&models.TelegramAgentSkill{}).
		Where("name = ?", skill.Name).
		Update("enabled", boolToTelegramSkillEnabledInt(enabled)).Error; err != nil {
		return TelegramAgentSkillView{}, err
	}
	skill, err = findTelegramAgentSkill(ctx, cfg, name)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	return toTelegramAgentSkillView(skill), nil
}

func boolToTelegramSkillEnabledInt(enabled bool) int {
	if enabled {
		return 1
	}
	return 0
}

func ImportTelegramAgentSkill(ctx context.Context, cfg models.TelegramAgentConfig, req TelegramAgentSkillImportRequest) (TelegramAgentSkillView, error) {
	source, err := normalizeTelegramAgentSkillImportSource(req.SourcePath)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	stat, err := os.Stat(source)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	root, err := resolveTelegramAgentSkillsRoot(cfg)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return TelegramAgentSkillView{}, err
	}

	skillName := strings.TrimSpace(req.Name)
	if skillName == "" {
		skillName = inferTelegramAgentSkillImportName(source, stat)
	}
	if err := validateTelegramAgentSkillName(skillName); err != nil {
		return TelegramAgentSkillView{}, err
	}
	target := filepath.Join(root, skillName)
	if same, err := sameTelegramAgentSkillPath(source, target); err == nil && same {
		return TelegramAgentSkillView{}, errors.New("导入源和目标目录相同")
	}
	if _, err := os.Stat(target); err == nil {
		if !req.Overwrite {
			if skill, parseErr := parseTelegramAgentSkillFromDir(target); parseErr == nil {
				return toTelegramAgentSkillView(skill), nil
			}
			return TelegramAgentSkillView{}, fmt.Errorf("Skill 已存在：%s", skillName)
		}
		if err := os.RemoveAll(target); err != nil {
			return TelegramAgentSkillView{}, err
		}
	} else if !os.IsNotExist(err) {
		return TelegramAgentSkillView{}, err
	}

	if stat.IsDir() {
		if !hasTelegramSkillFile(source) {
			return TelegramAgentSkillView{}, errors.New("导入目录中未找到 skills.md 或 SKILL.md")
		}
		if err := copyTelegramAgentSkillDir(source, target); err != nil {
			return TelegramAgentSkillView{}, err
		}
	} else {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return TelegramAgentSkillView{}, err
		}
		targetFile := filepath.Join(target, "SKILL.md")
		if err := copyTelegramAgentSkillFile(source, targetFile); err != nil {
			return TelegramAgentSkillView{}, err
		}
	}

	skill, err := parseTelegramAgentSkillFromDir(target)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	if models.DB != nil {
		record, err := upsertTelegramAgentSkillRecord(ctx, skill)
		if err != nil {
			return TelegramAgentSkillView{}, err
		}
		skill.Enabled = record.Enabled != 0
	}
	return toTelegramAgentSkillView(skill), nil
}

func parseTelegramAgentSkillFromDir(dir string) (telegramAgentSkill, error) {
	file, ok := findTelegramSkillFile(dir)
	if !ok {
		return telegramAgentSkill{}, fmt.Errorf("目录中未找到 skills.md 或 SKILL.md：%s", dir)
	}
	return parseTelegramAgentSkillFile(dir, file)
}

func scanTelegramAgentSkillsFromRoot(root string) ([]telegramAgentSkill, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return []telegramAgentSkill{}, nil
	}
	if stat, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return []telegramAgentSkill{}, nil
		}
		return nil, err
	} else if !stat.IsDir() {
		return nil, fmt.Errorf("Skills 路径不是目录：%s", root)
	}

	dirs := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if hasTelegramSkillFile(path) {
			dirs = append(dirs, path)
			if path != root {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	skills := make([]telegramAgentSkill, 0, len(dirs))
	for _, dir := range dirs {
		file, ok := findTelegramSkillFile(dir)
		if !ok {
			continue
		}
		skill, err := parseTelegramAgentSkillFile(dir, file)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return strings.ToLower(skills[i].Name) < strings.ToLower(skills[j].Name)
	})
	return skills, nil
}

func filterTelegramAgentSkillViews(skills []telegramAgentSkill, query string) ([]TelegramAgentSkillView, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		views := make([]TelegramAgentSkillView, 0, len(skills))
		for _, skill := range skills {
			views = append(views, toTelegramAgentSkillView(skill))
		}
		return views, nil
	}

	keyword := strings.ToLower(query)
	views := make([]TelegramAgentSkillView, 0, len(skills))
	for _, skill := range skills {
		if telegramAgentSkillMatches(skill, keyword) {
			views = append(views, toTelegramAgentSkillView(skill))
		}
	}
	return views, nil
}

func toTelegramAgentSkillView(skill telegramAgentSkill) TelegramAgentSkillView {
	scripts := make([]TelegramAgentSkillScriptView, 0, len(skill.Scripts))
	for _, script := range skill.Scripts {
		scripts = append(scripts, TelegramAgentSkillScriptView{
			Name:        script.Name,
			Path:        script.Path,
			Description: script.Description,
			TimeoutMs:   script.TimeoutMs,
			Usage:       append([]string{}, script.Usage...),
		})
	}
	return TelegramAgentSkillView{
		Name:         skill.Name,
		Description:  skill.Description,
		Enabled:      skill.Enabled,
		Dir:          skill.Dir,
		File:         skill.File,
		Instructions: skill.Instructions,
		Triggers:     append([]string{}, skill.Triggers...),
		Scripts:      scripts,
	}
}

func buildTelegramAgentSkillFileNodes(root string, relDir string, count *int) ([]TelegramAgentSkillFileNode, error) {
	current := root
	if relDir != "" {
		current = filepath.Join(root, relDir)
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		return nil, err
	}
	nodes := make([]TelegramAgentSkillFileNode, 0, len(entries))
	for _, entry := range entries {
		*count += 1
		if *count > telegramAgentSkillMaxFileNodes {
			return nil, fmt.Errorf("Skill 文件数量超过 %d 个，暂不支持在线展示", telegramAgentSkillMaxFileNodes)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		rel := entry.Name()
		if relDir != "" {
			rel = filepath.Join(relDir, entry.Name())
		}
		node := TelegramAgentSkillFileNode{
			Name:       entry.Name(),
			Path:       filepath.ToSlash(rel),
			Kind:       "file",
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
			Children:   []TelegramAgentSkillFileNode{},
		}
		if entry.IsDir() {
			node.Kind = "directory"
			children, err := buildTelegramAgentSkillFileNodes(root, rel, count)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind == "directory"
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	return nodes, nil
}

func resolveTelegramAgentSkillFilePath(skill telegramAgentSkill, relPath string) (string, string, error) {
	relPath = strings.TrimSpace(strings.ReplaceAll(relPath, "\\", "/"))
	if relPath == "" {
		return "", "", errors.New("文件路径不能为空")
	}
	cleanRel := filepath.Clean(relPath)
	if filepath.IsAbs(cleanRel) || cleanRel == "." || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("文件路径不安全：" + relPath)
	}
	target := filepath.Join(skill.Dir, cleanRel)
	if err := ensureTelegramSkillPathInside(skill.Dir, target); err != nil {
		return "", "", err
	}
	return filepath.Clean(target), filepath.ToSlash(cleanRel), nil
}

func isTelegramAgentSkillTextFile(raw []byte) bool {
	return bytes.IndexByte(raw, 0) < 0 && utf8.Valid(raw)
}

func telegramAgentSkillSearchText(skill telegramAgentSkill) string {
	parts := []string{skill.Name, skill.Description, strings.Join(skill.Triggers, " "), skill.Instructions}
	for _, script := range skill.Scripts {
		parts = append(parts, script.Name, script.Description, strings.Join(script.Usage, " "))
	}
	return strings.Join(parts, "\n")
}

func normalizeTelegramAgentSkillImportSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", errors.New("请填写本地 Skill 来源路径")
	}
	if !filepath.IsAbs(source) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		source = filepath.Join(cwd, source)
	}
	return filepath.Clean(source), nil
}

func inferTelegramAgentSkillImportName(source string, stat os.FileInfo) string {
	if stat.IsDir() {
		return filepath.Base(source)
	}
	name := filepath.Base(source)
	ext := filepath.Ext(name)
	if ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	if strings.EqualFold(name, "skill") || strings.EqualFold(name, "skills") {
		return filepath.Base(filepath.Dir(source))
	}
	return name
}

func validateTelegramAgentSkillName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Skill 名称不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("Skill 名称不能包含路径分隔符：%s", name)
	}
	return nil
}

func sameTelegramAgentSkillPath(left string, right string) (bool, error) {
	leftAbs, err := filepath.Abs(left)
	if err != nil {
		return false, err
	}
	rightAbs, err := filepath.Abs(right)
	if err != nil {
		return false, err
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs), nil
}

func copyTelegramAgentSkillDir(source string, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		return copyTelegramAgentSkillFile(path, dest)
	})
}

func copyTelegramAgentSkillFile(source string, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
