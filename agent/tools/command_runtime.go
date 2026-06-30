package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/racio/orvion/models"
)

const (
	telegramAgentCommandDefaultTimeoutMs = 30000
	telegramAgentCommandMaxTimeoutMs     = 240000
	telegramAgentCommandMaxOutputBytes   = 64 * 1024
	telegramAgentCommandMaxStdinBytes    = 64 * 1024
	telegramAgentCommandMaxArgs          = 64
	telegramAgentCommandMaxArgBytes      = 4096
)

type telegramCommandRun struct {
	Command    string   `json:"command"`
	Args       []string `json:"args,omitempty"`
	WorkingDir string   `json:"working_dir"`
	Stdin      string   `json:"stdin,omitempty"`
	TimeoutMs  int      `json:"timeout_ms"`
	SkillName  string   `json:"skill_name,omitempty"`
	ScriptName string   `json:"script_name,omitempty"`
	ScriptPath string   `json:"script_path,omitempty"`
}

func buildTelegramRunCommandAction(ctx context.Context, chatID int64, cfg models.TelegramAgentConfig, args CallArgs) (telegramToolAction, error) {
	run, err := normalizeTelegramCommandRun(args)
	if err != nil {
		return telegramToolAction{}, err
	}

	if skill, script, ok, err := matchTelegramCommandSkillScript(ctx, cfg, run); err != nil {
		return telegramToolAction{}, err
	} else if ok {
		if !skill.Enabled {
			return telegramToolAction{}, fmt.Errorf("Skill 已禁用：%s", skill.Name)
		}
		run.SkillName = skill.Name
		run.ScriptName = script.Name
		run.ScriptPath = script.AbsPath
	}

	summary := fmt.Sprintf("执行命令：%s\n工作目录：%s\n超时：%dms", formatTelegramCommandLine(run.Command, run.Args), run.WorkingDir, run.TimeoutMs)
	if run.SkillName != "" && run.ScriptName != "" {
		summary = fmt.Sprintf("执行 Skill 命令：%s/%s\n命令：%s\n工作目录：%s\n超时：%dms", run.SkillName, run.ScriptName, formatTelegramCommandLine(run.Command, run.Args), run.WorkingDir, run.TimeoutMs)
	}
	if strings.TrimSpace(run.Stdin) != "" {
		summary += fmt.Sprintf("\nstdin：%d 字节", len(run.Stdin))
	}

	return telegramToolAction{
		ChatID:     chatID,
		Kind:       telegramToolActionRunTerminalCommand,
		CommandRun: run,
		Summary:    summary,
		CreatedAt:  time.Now(),
	}, nil
}

func normalizeTelegramCommandRun(args CallArgs) (telegramCommandRun, error) {
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return telegramCommandRun{}, errors.New("请写明要执行的 command")
	}
	if strings.ContainsAny(command, "\x00\r\n\t ") {
		return telegramCommandRun{}, errors.New("command 必须是单个可执行文件名或路径，参数请放到 command_args")
	}

	commandArgs := make([]string, 0, len(args.CommandArgs))
	if len(args.CommandArgs) > telegramAgentCommandMaxArgs {
		return telegramCommandRun{}, fmt.Errorf("command_args 最多支持 %d 个参数", telegramAgentCommandMaxArgs)
	}
	for _, value := range args.CommandArgs {
		if strings.ContainsAny(value, "\x00\r\n") {
			return telegramCommandRun{}, errors.New("command_args 不能包含换行或空字符")
		}
		if len(value) > telegramAgentCommandMaxArgBytes {
			return telegramCommandRun{}, fmt.Errorf("单个命令参数不能超过 %d 字节", telegramAgentCommandMaxArgBytes)
		}
		commandArgs = append(commandArgs, value)
	}
	if isTelegramShellCommand(command) && len(commandArgs) > 0 && isTelegramShellEvalArg(commandArgs[0]) {
		return telegramCommandRun{}, errors.New("不允许使用 shell -c 执行拼接命令；请直接传脚本路径和参数")
	}

	workingDir := strings.TrimSpace(args.WorkingDir)
	if workingDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return telegramCommandRun{}, err
		}
		workingDir = cwd
	}
	if !filepath.IsAbs(workingDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return telegramCommandRun{}, err
		}
		workingDir = filepath.Join(cwd, workingDir)
	}
	workingDir = filepath.Clean(workingDir)
	stat, err := os.Stat(workingDir)
	if err != nil {
		return telegramCommandRun{}, fmt.Errorf("工作目录不可用: %w", err)
	}
	if !stat.IsDir() {
		return telegramCommandRun{}, errors.New("working_dir 必须是目录")
	}

	stdin := ""
	if args.Stdin != nil {
		stdin = *args.Stdin
		if len(stdin) > telegramAgentCommandMaxStdinBytes {
			return telegramCommandRun{}, fmt.Errorf("stdin 不能超过 %d 字节", telegramAgentCommandMaxStdinBytes)
		}
	}

	return telegramCommandRun{
		Command:    command,
		Args:       commandArgs,
		WorkingDir: workingDir,
		Stdin:      stdin,
		TimeoutMs:  normalizeTelegramCommandTimeoutMs(args.TimeoutMs),
	}, nil
}

func executeTelegramCommandAction(ctx context.Context, run telegramCommandRun) (string, error) {
	run.Command = strings.TrimSpace(run.Command)
	run.WorkingDir = filepath.Clean(strings.TrimSpace(run.WorkingDir))
	if run.Command == "" || run.WorkingDir == "" {
		return "", errors.New("命令执行参数不完整")
	}

	timeout := time.Duration(normalizeTelegramCommandTimeoutMs(run.TimeoutMs)) * time.Millisecond
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, run.Command, run.Args...)
	cmd.Dir = run.WorkingDir
	if run.Stdin != "" {
		cmd.Stdin = strings.NewReader(run.Stdin)
	}
	cmd.Env = append(os.Environ(),
		"ORVION_AGENT_TOOL="+NameRunTerminalCommand,
	)
	if run.SkillName != "" {
		cmd.Env = append(cmd.Env, "ORVION_SKILL_NAME="+run.SkillName)
	}
	if run.ScriptName != "" {
		cmd.Env = append(cmd.Env, "ORVION_SKILL_SCRIPT="+run.ScriptName)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stdoutText := truncateTelegramSkillText(stdout.String(), telegramAgentCommandMaxOutputBytes)
	stderrText := truncateTelegramSkillText(stderr.String(), telegramAgentCommandMaxOutputBytes/4)
	if execCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("命令执行超时（%dms）", normalizeTelegramCommandTimeoutMs(run.TimeoutMs))
	}

	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		result := formatTelegramCommandResult(run, exitCode, stdoutText, stderrText)
		return "", errors.New(result)
	}
	return formatTelegramCommandResult(run, exitCode, stdoutText, stderrText), nil
}

func matchTelegramCommandSkillScript(ctx context.Context, cfg models.TelegramAgentConfig, run telegramCommandRun) (telegramAgentSkill, telegramAgentSkillScript, bool, error) {
	if !telegramAgentSkillsEnabled(cfg) {
		return telegramAgentSkill{}, telegramAgentSkillScript{}, false, nil
	}
	skills, err := scanTelegramAgentSkills(ctx, cfg)
	if err != nil {
		return telegramAgentSkill{}, telegramAgentSkillScript{}, false, err
	}
	candidates := telegramCommandPathCandidates(run)
	for _, skill := range skills {
		for _, script := range skill.Scripts {
			for _, candidate := range candidates {
				if sameTelegramCommandPath(candidate, script.AbsPath) {
					return skill, script, true, nil
				}
			}
		}
	}
	return telegramAgentSkill{}, telegramAgentSkillScript{}, false, nil
}

func telegramCommandPathCandidates(run telegramCommandRun) []string {
	values := make([]string, 0, len(run.Args)+1)
	values = append(values, run.Command)
	values = append(values, run.Args...)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			continue
		}
		if filepath.IsAbs(value) {
			result = append(result, filepath.Clean(value))
			continue
		}
		if strings.ContainsRune(value, filepath.Separator) || strings.Contains(value, "/") {
			result = append(result, filepath.Clean(filepath.Join(run.WorkingDir, value)))
		}
	}
	return result
}

func sameTelegramCommandPath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}
	leftEval, leftErr := filepath.EvalSymlinks(left)
	rightEval, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftEval) == filepath.Clean(rightEval)
}

func formatTelegramCommandResult(run telegramCommandRun, exitCode int, stdoutText string, stderrText string) string {
	lines := []string{
		"已执行命令",
		"命令：" + formatTelegramCommandLine(run.Command, run.Args),
		"工作目录：" + run.WorkingDir,
		fmt.Sprintf("退出码：%d", exitCode),
	}
	if run.SkillName != "" && run.ScriptName != "" {
		lines = append(lines, "Skill："+run.SkillName, "脚本："+run.ScriptName)
	}
	if strings.TrimSpace(stdoutText) != "" {
		lines = append(lines, "stdout：", strings.TrimSpace(stdoutText))
	}
	if strings.TrimSpace(stderrText) != "" {
		lines = append(lines, "stderr：", strings.TrimSpace(stderrText))
	}
	if strings.TrimSpace(stdoutText) == "" && strings.TrimSpace(stderrText) == "" {
		lines = append(lines, "输出：无")
	}
	return strings.Join(lines, "\n")
}

func formatTelegramCommandLine(command string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, maskTelegramCommandArg(command, ""))
	for index, arg := range args {
		prev := ""
		if index > 0 {
			prev = args[index-1]
		}
		parts = append(parts, maskTelegramCommandArg(arg, prev))
	}
	return strings.Join(parts, " ")
}

func maskTelegramCommandArg(value string, prev string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "''"
	}
	if isTelegramAgentSensitiveLogKey(prev) || strings.Contains(strings.ToLower(prev), "key") {
		return "已隐藏"
	}
	if strings.Contains(trimmed, "=") {
		key, rawValue, _ := strings.Cut(trimmed, "=")
		if isTelegramAgentSensitiveLogKey(key) {
			return key + "=已隐藏"
		}
		trimmed = key + "=" + rawValue
	}
	if strings.ContainsAny(trimmed, " \t\"'") {
		return strconvQuoteTelegramCommandArg(trimmed)
	}
	return trimmed
}

func strconvQuoteTelegramCommandArg(value string) string {
	escaped := strings.ReplaceAll(value, `'`, `'\''`)
	return "'" + escaped + "'"
}

func isTelegramShellCommand(command string) bool {
	base := strings.ToLower(filepath.Base(command))
	switch base {
	case "sh", "bash", "zsh":
		return true
	default:
		return false
	}
}

func isTelegramShellEvalArg(arg string) bool {
	switch strings.TrimSpace(arg) {
	case "-c", "--command":
		return true
	default:
		return false
	}
}

func normalizeTelegramCommandTimeoutMs(value int) int {
	if value <= 0 {
		return telegramAgentCommandDefaultTimeoutMs
	}
	if value > telegramAgentCommandMaxTimeoutMs {
		return telegramAgentCommandMaxTimeoutMs
	}
	return value
}
