export const sectionTabs = [
  { id: "home", label: "首页" },
  { id: "claude", label: "Claude" },
  { id: "openai", label: "OpenAI" },
  { id: "gemini", label: "Gemini" },
  { id: "more", label: "更多" },
] as const;

export type SectionId = (typeof sectionTabs)[number]["id"];

export type CodeLang = "json" | "toml" | "env" | "bash";

export type CliSectionItem = {
  id: "claude" | "openai" | "gemini";
  badge: string;
  title: string;
  description: string;
  installLabel: string;
  installHint: string;
  installCommand: string;
  configPath: string;
  configContent: string;
  configLang: Exclude<CodeLang, "bash">;
  extraConfigPath?: string;
  extraConfigContent?: string;
  extraConfigLang?: Exclude<CodeLang, "bash">;
  reverse?: boolean;
};

export const cliSections: CliSectionItem[] = [
  {
    id: "claude",
    badge: "IDE 集成",
    title: "Claude Code",
    description:
      "直接在终端中释放 Claude 的原始能力。快速搜索代码库，串联复杂流程，让开发与调试更贴近思维速度。",
    installLabel: "Mac / Linux",
    installHint: "Terminal",
    installCommand: "curl -fsSL https://claude.ai/install.sh | bash",
    configPath: "~/.claude/settings.json",
    configContent: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "your-api-key",
    "ANTHROPIC_BASE_URL": "https://your-domain"
  }
}`,
    configLang: "json",
    reverse: true,
  },
  {
    id: "openai",
    badge: "命令行工具",
    title: "Codex CLI",
    description:
      "Codex CLI 是可在本地终端运行的编程助手，能够读取、修改并执行指定目录中的代码任务。",
    installLabel: "Node.js",
    installHint: "npm",
    installCommand: "npm install -g @openai/codex",
    configPath: "~/.codex/config.toml",
    configContent: `model_provider = "orvion"
model = "latest-model-name"
model_reasoning_effort = "high"
network_access = "enabled"
disable_response_storage = true

[model_providers.orvion]
name = "OpenAI"
base_url = "https://your-domain/v1"
wire_api = "responses"
requires_openai_auth = true`,
    configLang: "toml",
    extraConfigPath: "~/.codex/auth.json",
    extraConfigContent: `{
  "OPENAI_API_KEY": "your-api-key"
}`,
    extraConfigLang: "json",
  },
  {
    id: "gemini",
    badge: "多模态 AI",
    title: "Gemini CLI",
    description:
      "Gemini CLI 将 Gemini 的能力直接带入终端，提供轻量且直接的调用路径，适合自动化脚本与日常开发协作。",
    installLabel: "Node.js",
    installHint: "npm",
    installCommand: "npm install -g @google/gemini-cli",
    configPath: "~/.gemini/.env",
    configContent: `GOOGLE_GEMINI_BASE_URL=https://your-domain
GEMINI_API_KEY=your-api-key
GEMINI_MODEL=latest-model-name`,
    configLang: "env",
    extraConfigPath: "~/.gemini/settings.json",
    extraConfigContent: `{
  "ide": {
    "enabled": true
  },
  "security": {
    "auth": {
      "selectedType": "gemini-api-key"
    }
  }
}`,
    extraConfigLang: "json",
    reverse: true,
  },
];
