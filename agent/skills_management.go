package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/racio/orvion/consts"
	"github.com/racio/orvion/models"
	"github.com/racio/orvion/providers"
	runtimesvc "github.com/racio/orvion/service/runtime"
	"gorm.io/gorm"
)

const (
	TelegramAgentSkillSearchKeyword   = "keyword"
	TelegramAgentSkillSearchEmbedding = "embedding"

	telegramAgentSkillEmbeddingDims = 256
	telegramAgentSkillFileMaxBytes  = 1024 * 1024
	telegramAgentSkillMaxFileNodes  = 2000
)

type TelegramAgentSkillScriptView struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Confirm     bool   `json:"confirm"`
	TimeoutMs   int    `json:"timeout_ms"`
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
	Score        float64                        `json:"score,omitempty"`
	Installed    bool                           `json:"installed,omitempty"`
}

type TelegramAgentSkillListResult struct {
	Skills        []TelegramAgentSkillView `json:"skills"`
	Total         int                      `json:"total"`
	SkillsEnabled bool                     `json:"skills_enabled"`
	Query         string                   `json:"query"`
	SearchMode    string                   `json:"search_mode"`
	ScannedAt     time.Time                `json:"scanned_at"`
}

type TelegramAgentSkillReloadResult struct {
	TelegramAgentSkillListResult
	Message string `json:"message"`
}

type TelegramAgentSkillMarketResult struct {
	Skills     []TelegramAgentSkillView `json:"skills"`
	Total      int                      `json:"total"`
	MarketDirs []string                 `json:"market_dirs"`
	ScannedAt  time.Time                `json:"scanned_at"`
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

func ListTelegramAgentSkillsForManagement(ctx context.Context, cfg models.TelegramAgentConfig, query string, searchMode string) (TelegramAgentSkillListResult, error) {
	_ = ctx
	skills, err := scanTelegramAgentSkills(cfg)
	if err != nil {
		return TelegramAgentSkillListResult{}, err
	}
	filtered, err := filterTelegramAgentSkillViews(ctx, cfg, skills, query, searchMode)
	if err != nil {
		return TelegramAgentSkillListResult{}, err
	}
	return TelegramAgentSkillListResult{
		Skills:        filtered,
		Total:         len(filtered),
		SkillsEnabled: telegramAgentSkillsEnabled(cfg),
		Query:         strings.TrimSpace(query),
		SearchMode:    normalizeTelegramAgentSkillSearchMode(searchMode),
		ScannedAt:     time.Now(),
	}, nil
}

func ReloadTelegramAgentSkillsForManagement(ctx context.Context, cfg models.TelegramAgentConfig, query string, searchMode string) (TelegramAgentSkillReloadResult, error) {
	result, err := ListTelegramAgentSkillsForManagement(ctx, cfg, query, searchMode)
	if err != nil {
		return TelegramAgentSkillReloadResult{}, err
	}
	return TelegramAgentSkillReloadResult{
		TelegramAgentSkillListResult: result,
		Message:                      fmt.Sprintf("已重新扫描本地 Skills，共 %d 个", result.Total),
	}, nil
}

func ReadTelegramAgentSkillForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string) (TelegramAgentSkillView, error) {
	_ = ctx
	skill, err := findTelegramAgentSkill(cfg, name)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	return toTelegramAgentSkillView(skill, 0), nil
}

func ListTelegramAgentSkillFilesForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string) (TelegramAgentSkillFileTreeResult, error) {
	_ = ctx
	skill, err := findTelegramAgentSkill(cfg, name)
	if err != nil {
		return TelegramAgentSkillFileTreeResult{}, err
	}
	count := 0
	files, err := buildTelegramAgentSkillFileNodes(skill.Dir, "", &count)
	if err != nil {
		return TelegramAgentSkillFileTreeResult{}, err
	}
	return TelegramAgentSkillFileTreeResult{
		Skill: toTelegramAgentSkillView(skill, 0),
		Root:  skill.Dir,
		Files: files,
	}, nil
}

func ReadTelegramAgentSkillFileForManagement(ctx context.Context, cfg models.TelegramAgentConfig, name string, relPath string) (TelegramAgentSkillFileContentResult, error) {
	_ = ctx
	skill, err := findTelegramAgentSkill(cfg, name)
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
	_ = ctx
	skill, err := findTelegramAgentSkill(cfg, name)
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
	_ = ctx
	root, err := resolveTelegramAgentSkillsRoot(cfg)
	if err != nil {
		return TelegramAgentSkillDeleteResult{}, err
	}
	skill, err := findTelegramAgentSkill(cfg, name)
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
	return TelegramAgentSkillDeleteResult{
		Name:    skill.Name,
		Dir:     skill.Dir,
		Message: fmt.Sprintf("已移除 Skill：%s", skill.Name),
	}, nil
}

func SetTelegramAgentSkillEnabled(ctx context.Context, cfg models.TelegramAgentConfig, name string, enabled bool) (TelegramAgentSkillView, error) {
	_ = ctx
	skill, err := findTelegramAgentSkill(cfg, name)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	if err := writeTelegramAgentSkillEnabled(skill.File, skill.Name, enabled); err != nil {
		return TelegramAgentSkillView{}, err
	}
	skill, err = findTelegramAgentSkill(cfg, name)
	if err != nil {
		return TelegramAgentSkillView{}, err
	}
	return toTelegramAgentSkillView(skill, 0), nil
}

func ListTelegramAgentSkillMarket(ctx context.Context, cfg models.TelegramAgentConfig, query string) (TelegramAgentSkillMarketResult, error) {
	_ = ctx
	installed, err := scanTelegramAgentSkills(cfg)
	if err != nil {
		return TelegramAgentSkillMarketResult{}, err
	}
	installedNames := make(map[string]struct{}, len(installed))
	for _, skill := range installed {
		installedNames[strings.ToLower(skill.Name)] = struct{}{}
	}

	marketDirs := telegramAgentSkillMarketDirs(cfg)
	items := make([]TelegramAgentSkillView, 0)
	for _, dir := range marketDirs {
		skills, err := scanTelegramAgentSkillsFromRoot(dir)
		if err != nil {
			return TelegramAgentSkillMarketResult{}, err
		}
		for _, skill := range skills {
			if strings.TrimSpace(query) != "" && !telegramAgentSkillMatches(skill, strings.ToLower(strings.TrimSpace(query))) {
				continue
			}
			view := toTelegramAgentSkillView(skill, 0)
			_, view.Installed = installedNames[strings.ToLower(skill.Name)]
			items = append(items, view)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Installed != items[j].Installed {
			return !items[i].Installed
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return TelegramAgentSkillMarketResult{
		Skills:     items,
		Total:      len(items),
		MarketDirs: marketDirs,
		ScannedAt:  time.Now(),
	}, nil
}

func ImportTelegramAgentSkill(ctx context.Context, cfg models.TelegramAgentConfig, req TelegramAgentSkillImportRequest) (TelegramAgentSkillView, error) {
	_ = ctx
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
				return toTelegramAgentSkillView(skill, 0), nil
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
	return toTelegramAgentSkillView(skill, 0), nil
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
	if hasTelegramSkillFile(root) {
		dirs = append(dirs, root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
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

func filterTelegramAgentSkillViews(ctx context.Context, cfg models.TelegramAgentConfig, skills []telegramAgentSkill, query string, searchMode string) ([]TelegramAgentSkillView, error) {
	query = strings.TrimSpace(query)
	mode := normalizeTelegramAgentSkillSearchMode(searchMode)
	if query == "" {
		views := make([]TelegramAgentSkillView, 0, len(skills))
		for _, skill := range skills {
			views = append(views, toTelegramAgentSkillView(skill, 0))
		}
		return views, nil
	}

	if mode == TelegramAgentSkillSearchEmbedding {
		queryVector, err := buildTelegramAgentSkillEmbedding(ctx, cfg, query)
		if err != nil {
			return nil, err
		}
		views := make([]TelegramAgentSkillView, 0, len(skills))
		for _, skill := range skills {
			skillVector, err := buildTelegramAgentSkillEmbedding(ctx, cfg, telegramAgentSkillSearchText(skill))
			if err != nil {
				return nil, err
			}
			score := telegramAgentSkillCosine(queryVector, skillVector)
			if score <= 0 {
				continue
			}
			views = append(views, toTelegramAgentSkillView(skill, score))
		}
		sort.SliceStable(views, func(i, j int) bool {
			if views[i].Score == views[j].Score {
				return strings.ToLower(views[i].Name) < strings.ToLower(views[j].Name)
			}
			return views[i].Score > views[j].Score
		})
		return views, nil
	}

	keyword := strings.ToLower(query)
	views := make([]TelegramAgentSkillView, 0, len(skills))
	for _, skill := range skills {
		if telegramAgentSkillMatches(skill, keyword) {
			views = append(views, toTelegramAgentSkillView(skill, 0))
		}
	}
	return views, nil
}

func toTelegramAgentSkillView(skill telegramAgentSkill, score float64) TelegramAgentSkillView {
	scripts := make([]TelegramAgentSkillScriptView, 0, len(skill.Scripts))
	for _, script := range skill.Scripts {
		scripts = append(scripts, TelegramAgentSkillScriptView{
			Name:        script.Name,
			Path:        script.Path,
			Description: script.Description,
			Confirm:     script.Confirm,
			TimeoutMs:   script.TimeoutMs,
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
		Score:        score,
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

func normalizeTelegramAgentSkillSearchMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TelegramAgentSkillSearchEmbedding:
		return TelegramAgentSkillSearchEmbedding
	default:
		return TelegramAgentSkillSearchKeyword
	}
}

func telegramAgentSkillSearchText(skill telegramAgentSkill) string {
	parts := []string{skill.Name, skill.Description, strings.Join(skill.Triggers, " "), skill.Instructions}
	for _, script := range skill.Scripts {
		parts = append(parts, script.Name, script.Description)
	}
	return strings.Join(parts, "\n")
}

func buildTelegramAgentSkillEmbedding(ctx context.Context, cfg models.TelegramAgentConfig, text string) ([]float64, error) {
	if strings.TrimSpace(cfg.SkillsEmbeddingModel) == "" {
		return telegramAgentSkillLocalEmbeddingVector(text), nil
	}
	vector, err := buildTelegramAgentRemoteSkillEmbedding(ctx, cfg, text)
	if err != nil {
		return nil, err
	}
	if len(vector) == 0 {
		return nil, errors.New("远端向量模型返回空 embedding")
	}
	return normalizeTelegramAgentSkillVector(vector), nil
}

func buildTelegramAgentRemoteSkillEmbedding(ctx context.Context, cfg models.TelegramAgentConfig, text string) ([]float64, error) {
	selected, err := selectTelegramAgentEmbeddingModelProvider(ctx, cfg.SkillsEmbeddingModel)
	if err != nil {
		return nil, err
	}
	body, requestCtx, err := buildTelegramAgentEmbeddingRequestBody(ctx, selected.ProviderStyle, text)
	if err != nil {
		return nil, err
	}
	provider, err := providers.NewForStyleWithProxy(selected.ProviderStyle, selected.ProviderConfig, selected.ProviderProxy)
	if err != nil {
		return nil, err
	}
	header := runtimesvc.BuildHeaders(nil, selected.WithHeader, selected.CustomerHeaders, false)
	req, err := provider.BuildReq(requestCtx, header, selected.ProviderModel, body)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(selected.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client, err := providers.GetClientWithProxy(timeout, selected.ProviderProxy)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s/%s embeddings 返回状态 %d: %s", selected.ProviderName, selected.ProviderModel, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	vector := parseTelegramAgentEmbeddingVector(selected.ProviderStyle, raw)
	if len(vector) == 0 {
		return nil, errors.New("无法解析远端向量模型响应")
	}
	return vector, nil
}

func selectTelegramAgentEmbeddingModelProvider(ctx context.Context, modelName string) (selectedModelProvider, error) {
	if models.DB == nil {
		return selectedModelProvider{}, errors.New("数据库未初始化")
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return selectedModelProvider{}, errors.New("未配置 Skills 向量模型")
	}

	var model models.Model
	if err := models.DB.WithContext(ctx).Where("status = ? AND name = ?", 1, modelName).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return selectedModelProvider{}, fmt.Errorf("未找到可用向量模型：%s", modelName)
		}
		return selectedModelProvider{}, err
	}

	var associations []models.ModelWithProvider
	now := time.Now()
	if err := models.DB.WithContext(ctx).
		Where("model_id = ? AND status = ?", model.ID, 1).
		Where("(auto_disabled_until IS NULL OR auto_disabled_until <= ?)", now).
		Order("weight DESC, id ASC").
		Find(&associations).Error; err != nil {
		return selectedModelProvider{}, err
	}
	if len(associations) == 0 {
		return selectedModelProvider{}, fmt.Errorf("向量模型 %s 没有可用提供商", model.Name)
	}

	providerIDs := make([]uint, 0, len(associations))
	for _, item := range associations {
		providerIDs = append(providerIDs, item.ProviderID)
	}
	var providerList []models.Provider
	if err := models.DB.WithContext(ctx).Where("id IN ?", providerIDs).Find(&providerList).Error; err != nil {
		return selectedModelProvider{}, err
	}
	providerByID := make(map[uint]models.Provider, len(providerList))
	for _, provider := range providerList {
		providerByID[provider.ID] = provider
	}

	for _, association := range associations {
		provider, ok := providerByID[association.ProviderID]
		if !ok {
			continue
		}
		if !models.ProviderSupportsEndpoint(provider.Capabilities, "embeddings") {
			continue
		}
		style := providers.ResolveStyle("", provider.Config)
		if style != consts.StyleOpenAI && style != consts.StyleGemini {
			continue
		}

		customHeaders := map[string]string{}
		if strings.TrimSpace(association.CustomerHeaders) != "" {
			_ = json.Unmarshal([]byte(association.CustomerHeaders), &customHeaders)
		}
		timeoutSeconds := model.TimeOut
		if timeoutSeconds <= 0 {
			timeoutSeconds = 30
		}
		return selectedModelProvider{
			ModelName:       model.Name,
			ProviderModel:   association.ProviderModel,
			ProviderName:    provider.Name,
			ModelProviderID: association.ID,
			ProviderConfig:  provider.Config,
			ProviderProxy:   provider.ProxyURL,
			ProviderStyle:   style,
			WithHeader:      association.WithHeader == 1,
			CustomerHeaders: customHeaders,
			TimeoutSeconds:  timeoutSeconds,
			IOLog:           model.IOLog == 1,
		}, nil
	}
	return selectedModelProvider{}, fmt.Errorf("向量模型 %s 没有支持 embeddings 的可用提供商", model.Name)
}

func buildTelegramAgentEmbeddingRequestBody(ctx context.Context, style string, text string) ([]byte, context.Context, error) {
	switch style {
	case consts.StyleGemini:
		body, err := json.Marshal(map[string]any{
			"content": map[string]any{
				"parts": []map[string]string{{"text": text}},
			},
		})
		return body, context.WithValue(ctx, consts.ContextKeyGeminiMethod, "embedContent"), err
	default:
		body, err := json.Marshal(map[string]any{"input": text})
		return body, context.WithValue(ctx, consts.ContextKeyOpenAIEndpoint, "embeddings"), err
	}
}

func parseTelegramAgentEmbeddingVector(style string, raw []byte) []float64 {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	root, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	var values any
	if style == consts.StyleGemini {
		if embedding, ok := root["embedding"].(map[string]any); ok {
			values = embedding["values"]
		}
	} else if data, ok := root["data"].([]any); ok && len(data) > 0 {
		if first, ok := data[0].(map[string]any); ok {
			values = first["embedding"]
		}
	}
	return floatsFromTelegramAgentEmbeddingValue(values)
}

func floatsFromTelegramAgentEmbeddingValue(value any) []float64 {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]float64, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case float64:
			result = append(result, typed)
		case float32:
			result = append(result, float64(typed))
		case int:
			result = append(result, float64(typed))
		}
	}
	return result
}

func normalizeTelegramAgentSkillVector(vector []float64) []float64 {
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	out := make([]float64, len(vector))
	for index := range vector {
		out[index] = vector[index] / norm
	}
	return out
}

func telegramAgentSkillLocalEmbeddingVector(text string) []float64 {
	vector := make([]float64, telegramAgentSkillEmbeddingDims)
	for _, token := range tokenizeTelegramAgentSkillText(text) {
		if token == "" {
			continue
		}
		index := telegramAgentSkillHashIndex(token)
		vector[index]++
	}
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return vector
	}
	norm = math.Sqrt(norm)
	for index := range vector {
		vector[index] /= norm
	}
	return normalizeTelegramAgentSkillVector(vector)
}

func tokenizeTelegramAgentSkillText(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	tokens := make([]string, 0, len(fields)*2)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		tokens = append(tokens, field)
		runes := []rune(field)
		if len(runes) >= 2 {
			for i := 0; i < len(runes)-1; i++ {
				tokens = append(tokens, string(runes[i:i+2]))
			}
		}
		if len(runes) >= 3 {
			for i := 0; i < len(runes)-2; i++ {
				tokens = append(tokens, string(runes[i:i+3]))
			}
		}
	}
	return tokens
}

func telegramAgentSkillHashIndex(token string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	return int(h.Sum32() % telegramAgentSkillEmbeddingDims)
}

func telegramAgentSkillCosine(left []float64, right []float64) float64 {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	var score float64
	for i := 0; i < limit; i++ {
		score += left[i] * right[i]
	}
	return score
}

func writeTelegramAgentSkillEnabled(file string, skillName string, enabled bool) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	enabledLine := fmt.Sprintf("enabled: %t", enabled)
	if !strings.HasPrefix(content, "---\n") {
		return os.WriteFile(file, []byte(strings.Join([]string{
			"---",
			"name: " + skillName,
			enabledLine,
			"---",
			content,
		}, "\n")), 0o644)
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return os.WriteFile(file, []byte(strings.Join([]string{
			"---",
			"name: " + skillName,
			enabledLine,
			"---",
			content,
		}, "\n")), 0o644)
	}
	metaEnd := 4 + end
	meta := content[4:metaEnd]
	rest := content[metaEnd:]
	lines := strings.Split(meta, "\n")
	replaced := false
	for index, line := range lines {
		key, _, ok := splitTelegramSkillKV(strings.TrimSpace(line))
		if ok && key == "enabled" {
			prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[index] = prefix + enabledLine
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append(lines, enabledLine)
	}
	next := "---\n" + strings.Join(lines, "\n") + rest
	return os.WriteFile(file, []byte(next), 0o644)
}

func telegramAgentSkillMarketDirs(cfg models.TelegramAgentConfig) []string {
	roots := []string{}
	if env := strings.TrimSpace(os.Getenv("ORVION_SKILL_MARKET_DIR")); env != "" {
		roots = append(roots, env)
	}
	cwd, err := os.Getwd()
	if err == nil {
		if root, err := resolveTelegramAgentSkillsRoot(cfg); err == nil {
			roots = append(roots, filepath.Join(filepath.Dir(root), "skills-market"))
		}
		roots = append(roots,
			filepath.Join(cwd, "data", "skills-market"),
			filepath.Join(cwd, "skills-market"),
			filepath.Join(cwd, "skill-market"),
			filepath.Join(cwd, "skills_market"),
		)
	}
	seen := make(map[string]struct{}, len(roots))
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		if !filepath.IsAbs(root) && cwd != "" {
			root = filepath.Join(cwd, root)
		}
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		result = append(result, root)
	}
	return result
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
