package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/racio/orvion/models"
)

const (
	telegramAgentDefaultSkillsDir      = "data/skills"
	telegramAgentSkillDefaultTimeoutMs = 10000
	telegramAgentSkillMaxTimeoutMs     = 120000
	telegramAgentSkillMaxOutputBytes   = 64 * 1024
	telegramAgentSkillListMaxLimit     = 50
)

type telegramAgentSkill struct {
	Name         string
	Description  string
	Enabled      bool
	Dir          string
	File         string
	Instructions string
	Triggers     []string
	Scripts      []telegramAgentSkillScript
}

type telegramAgentSkillScript struct {
	Name        string
	Path        string
	AbsPath     string
	Description string
	TimeoutMs   int
}

func telegramAgentSkillsEnabled(cfg models.TelegramAgentConfig) bool {
	return cfg.SkillsEnabled != nil && *cfg.SkillsEnabled
}

func listTelegramAgentSkills(ctx context.Context, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) (string, error) {
	if !telegramAgentSkillsEnabled(cfg) {
		return "Skills 未启用。请先在 TG Agent 配置中启用 Skills 并设置本地目录。", nil
	}
	result, err := ListTelegramAgentSkillsForManagement(ctx, cfg, args.Query, args.SearchMode)
	if err != nil {
		return "", err
	}
	if len(result.Skills) == 0 {
		return "暂无匹配 Skill", nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = telegramAgentToolListLimit
	}
	if limit > telegramAgentSkillListMaxLimit {
		limit = telegramAgentSkillListMaxLimit
	}
	if limit > len(result.Skills) {
		limit = len(result.Skills)
	}

	lines := []string{fmt.Sprintf("Skills 列表（显示 %d/%d 个）", limit, len(result.Skills))}
	if result.SearchMode == TelegramAgentSkillSearchEmbedding && strings.TrimSpace(args.Query) != "" {
		lines = append(lines, "检索方式：embedding")
	}
	for index, skill := range result.Skills[:limit] {
		status := "启用"
		if !skill.Enabled {
			status = "禁用"
		}
		lines = append(lines,
			fmt.Sprintf("%d. %s", index+1, skill.Name),
			"   状态："+status,
			"   描述："+emptyTextFallback(skill.Description, "无"),
			fmt.Sprintf("   脚本：%d 个", len(skill.Scripts)),
		)
		if skill.Score > 0 {
			lines = append(lines, fmt.Sprintf("   相似度：%.3f", skill.Score))
		}
		if len(skill.Triggers) > 0 {
			lines = append(lines, "   触发词："+strings.Join(skill.Triggers, "、"))
		}
	}
	if len(result.Skills) > limit {
		lines = append(lines, fmt.Sprintf("还有 %d 个未显示，可用 query 或 limit 缩小范围。", len(result.Skills)-limit))
	}
	return strings.Join(lines, "\n"), nil
}

func readTelegramAgentSkill(ctx context.Context, cfg models.TelegramAgentConfig, args telegramAgentToolCallArgs) (string, error) {
	if !telegramAgentSkillsEnabled(cfg) {
		return "Skills 未启用。请先在 TG Agent 配置中启用 Skills 并设置本地目录。", nil
	}
	skill, err := findTelegramAgentSkill(ctx, cfg, args.Skill)
	if err != nil {
		return "", err
	}
	lines := []string{
		"Skill：" + skill.Name,
		"状态：" + mapBoolText(skill.Enabled, "启用", "禁用"),
		"描述：" + emptyTextFallback(skill.Description, "无"),
		"Skill 目录：" + skill.Dir,
	}
	if len(skill.Triggers) > 0 {
		lines = append(lines, "触发词："+strings.Join(skill.Triggers, "、"))
	}
	if len(skill.Scripts) > 0 {
		lines = append(lines, "脚本：")
		for _, script := range skill.Scripts {
			lines = append(lines,
				fmt.Sprintf("- %s：%s（超时 %dms）", script.Name, emptyTextFallback(script.Description, "无描述"), script.TimeoutMs),
				"  相对路径："+filepath.ToSlash(script.Path),
				"  绝对路径："+script.AbsPath,
			)
		}
	} else {
		lines = append(lines, "脚本：无")
	}
	if strings.TrimSpace(skill.Instructions) != "" {
		lines = append(lines, "", "说明：", truncateTelegramSkillText(skill.Instructions, telegramAgentSkillMaxOutputBytes/2))
	}
	lines = append(lines,
		"",
		"命令行调用约定：",
		"1. 根据 Skill 说明决定脚本参数。",
		"2. 使用 run_terminal_command 执行脚本，working_dir 传 Skill 目录，脚本路径优先使用上面的绝对路径。",
		"3. shell 脚本通常使用 command=bash、command_args=[\"脚本绝对路径\", \"--参数\", \"值\"]；Python/Node 脚本分别使用 python3/node。",
		"4. 如果脚本明确要求 stdin JSON，可通过 stdin 传入；否则优先使用命令行参数。",
	)
	return strings.Join(lines, "\n"), nil
}

func scanTelegramAgentSkills(ctx context.Context, cfg models.TelegramAgentConfig) ([]telegramAgentSkill, error) {
	root, err := resolveTelegramAgentSkillsRoot(cfg)
	if err != nil {
		return nil, err
	}
	skills, err := scanTelegramAgentSkillsFromRoot(root)
	if err != nil {
		return nil, err
	}
	return syncTelegramAgentSkills(ctx, root, skills)
}

func resolveTelegramAgentSkillsRoot(cfg models.TelegramAgentConfig) (string, error) {
	root := strings.TrimSpace(cfg.SkillsDir)
	if root == "" {
		root = telegramAgentDefaultSkillsDir
	}
	if !filepath.IsAbs(root) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = filepath.Join(cwd, root)
	}
	return filepath.Clean(root), nil
}

func findTelegramAgentSkill(ctx context.Context, cfg models.TelegramAgentConfig, name string) (telegramAgentSkill, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return telegramAgentSkill{}, errors.New("请写明 Skill 名称")
	}
	skills, err := scanTelegramAgentSkills(ctx, cfg)
	if err != nil {
		return telegramAgentSkill{}, err
	}
	lowerName := strings.ToLower(name)
	var fuzzy []telegramAgentSkill
	for _, skill := range skills {
		if strings.ToLower(skill.Name) == lowerName {
			return skill, nil
		}
		if strings.Contains(strings.ToLower(skill.Name), lowerName) {
			fuzzy = append(fuzzy, skill)
		}
	}
	if len(fuzzy) == 1 {
		return fuzzy[0], nil
	}
	if len(fuzzy) > 1 {
		names := make([]string, 0, len(fuzzy))
		for _, skill := range fuzzy {
			names = append(names, skill.Name)
		}
		return telegramAgentSkill{}, fmt.Errorf("匹配到多个 Skill：%s", strings.Join(names, "、"))
	}
	return telegramAgentSkill{}, fmt.Errorf("未找到 Skill：%s", name)
}

func findTelegramAgentSkillScript(skill telegramAgentSkill, name string) (telegramAgentSkillScript, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return telegramAgentSkillScript{}, errors.New("请写明脚本名称")
	}
	lowerName := strings.ToLower(name)
	var fuzzy []telegramAgentSkillScript
	for _, script := range skill.Scripts {
		if strings.ToLower(script.Name) == lowerName {
			return script, nil
		}
		if strings.Contains(strings.ToLower(script.Name), lowerName) {
			fuzzy = append(fuzzy, script)
		}
	}
	if len(fuzzy) == 1 {
		return fuzzy[0], nil
	}
	if len(fuzzy) > 1 {
		names := make([]string, 0, len(fuzzy))
		for _, script := range fuzzy {
			names = append(names, script.Name)
		}
		return telegramAgentSkillScript{}, fmt.Errorf("匹配到多个脚本：%s", strings.Join(names, "、"))
	}
	return telegramAgentSkillScript{}, fmt.Errorf("Skill %s 中未找到脚本：%s", skill.Name, name)
}

func parseTelegramAgentSkillFile(dir string, file string) (telegramAgentSkill, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return telegramAgentSkill{}, err
	}
	meta, body := splitTelegramSkillFrontMatter(string(raw))
	skill := telegramAgentSkill{
		Name:         filepath.Base(dir),
		Enabled:      true,
		Dir:          filepath.Clean(dir),
		File:         filepath.Clean(file),
		Instructions: strings.TrimSpace(body),
	}
	parseTelegramSkillMeta(meta, &skill)
	if strings.TrimSpace(skill.Name) == "" {
		skill.Name = filepath.Base(dir)
	}
	skill.Scripts = normalizeTelegramSkillScripts(skill, skill.Scripts)
	if len(skill.Scripts) == 0 {
		skill.Scripts = discoverTelegramSkillScripts(skill)
	}
	return skill, nil
}

func splitTelegramSkillFrontMatter(content string) (string, string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", content
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return "", content
	}
	metaEnd := 4 + end
	bodyStart := metaEnd + len("\n---")
	if bodyStart < len(content) && content[bodyStart] == '\n' {
		bodyStart++
	}
	return content[4:metaEnd], content[bodyStart:]
}

func parseTelegramSkillMeta(meta string, skill *telegramAgentSkill) {
	lines := strings.Split(meta, "\n")
	inScripts := false
	var current *telegramAgentSkillScript
	flushScript := func() {
		if current == nil {
			return
		}
		if strings.TrimSpace(current.Name) != "" || strings.TrimSpace(current.Path) != "" {
			skill.Scripts = append(skill.Scripts, *current)
		}
		current = nil
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "scripts:" {
			inScripts = true
			continue
		}
		if inScripts {
			if strings.HasPrefix(trimmed, "- ") {
				flushScript()
				current = &telegramAgentSkillScript{TimeoutMs: telegramAgentSkillDefaultTimeoutMs}
				parseTelegramSkillScriptKV(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), current)
				continue
			}
			if current != nil && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
				parseTelegramSkillScriptKV(trimmed, current)
				continue
			}
			flushScript()
			inScripts = false
		}
		parseTelegramSkillKV(trimmed, skill)
	}
	flushScript()
}

func parseTelegramSkillKV(line string, skill *telegramAgentSkill) {
	key, value, ok := splitTelegramSkillKV(line)
	if !ok {
		return
	}
	switch key {
	case "name":
		skill.Name = value
	case "description":
		skill.Description = value
	case "triggers":
		skill.Triggers = parseTelegramSkillStringList(value)
	}
}

func parseTelegramSkillScriptKV(line string, script *telegramAgentSkillScript) {
	key, value, ok := splitTelegramSkillKV(line)
	if !ok {
		return
	}
	switch key {
	case "name":
		script.Name = value
	case "path":
		script.Path = value
	case "description":
		script.Description = value
	case "timeout_ms":
		if parsed, err := strconv.Atoi(value); err == nil {
			script.TimeoutMs = normalizeTelegramSkillTimeoutMs(parsed)
		}
	}
}

func splitTelegramSkillKV(line string) (string, string, bool) {
	left, right, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(left))
	value := unquoteTelegramSkillValue(strings.TrimSpace(right))
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func unquoteTelegramSkillValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func parseTelegramSkillStringList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(unquoteTelegramSkillValue(part))
		if item != "" {
			result = append(result, item)
		}
	}
	return orderedUniqueStrings(result)
}

func normalizeTelegramSkillScripts(skill telegramAgentSkill, scripts []telegramAgentSkillScript) []telegramAgentSkillScript {
	result := make([]telegramAgentSkillScript, 0, len(scripts))
	for _, script := range scripts {
		script.Name = strings.TrimSpace(script.Name)
		script.Path = strings.TrimSpace(script.Path)
		if script.Path == "" {
			continue
		}
		if script.Name == "" {
			script.Name = strings.TrimSuffix(filepath.Base(script.Path), filepath.Ext(script.Path))
		}
		script.TimeoutMs = normalizeTelegramSkillTimeoutMs(script.TimeoutMs)
		absPath := script.Path
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(skill.Dir, absPath)
		}
		script.AbsPath = filepath.Clean(absPath)
		if err := ensureTelegramSkillPathInside(skill.Dir, script.AbsPath); err != nil {
			continue
		}
		if stat, err := os.Stat(script.AbsPath); err != nil || stat.IsDir() {
			continue
		}
		result = append(result, script)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func discoverTelegramSkillScripts(skill telegramAgentSkill) []telegramAgentSkillScript {
	scriptsDir := filepath.Join(skill.Dir, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		return nil
	}
	scripts := make([]telegramAgentSkillScript, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(scriptsDir, entry.Name())
		if !isTelegramSkillScriptSupported(path) {
			continue
		}
		scripts = append(scripts, telegramAgentSkillScript{
			Name:      strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			Path:      filepath.ToSlash(filepath.Join("scripts", entry.Name())),
			AbsPath:   filepath.Clean(path),
			TimeoutMs: telegramAgentSkillDefaultTimeoutMs,
		})
	}
	sort.SliceStable(scripts, func(i, j int) bool {
		return strings.ToLower(scripts[i].Name) < strings.ToLower(scripts[j].Name)
	})
	return scripts
}

func hasTelegramSkillFile(dir string) bool {
	_, ok := findTelegramSkillFile(dir)
	return ok
}

func findTelegramSkillFile(dir string) (string, bool) {
	for _, name := range []string{"skills.md", "SKILL.md"} {
		path := filepath.Join(dir, name)
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			return filepath.Clean(path), true
		}
	}
	return "", false
}

func telegramAgentSkillMatches(skill telegramAgentSkill, keyword string) bool {
	values := []string{skill.Name, skill.Description}
	values = append(values, skill.Triggers...)
	for _, script := range skill.Scripts {
		values = append(values, script.Name, script.Description)
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), keyword) {
			return true
		}
	}
	return false
}

func ensureTelegramSkillPathInside(root string, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || rel == "" {
		return nil
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("脚本路径必须位于 Skill 目录内：%s", path)
	}
	return nil
}

func isTelegramSkillScriptSupported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sh", ".bash", ".py", ".js", ".mjs":
		return true
	default:
		return isTelegramSkillScriptExecutable(path)
	}
}

func isTelegramSkillScriptExecutable(path string) bool {
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() {
		return false
	}
	return stat.Mode()&0111 != 0
}

func normalizeTelegramSkillTimeoutMs(value int) int {
	if value <= 0 {
		return telegramAgentSkillDefaultTimeoutMs
	}
	if value > telegramAgentSkillMaxTimeoutMs {
		return telegramAgentSkillMaxTimeoutMs
	}
	return value
}

func truncateTelegramSkillText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...内容已截断"
}

func emptyTextFallback(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func mapBoolText(value bool, whenTrue string, whenFalse string) string {
	if value {
		return whenTrue
	}
	return whenFalse
}
