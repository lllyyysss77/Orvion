package consts

type Style = string

const (
	StyleOpenAI    Style = "openai"
	StyleOpenAIRes Style = "codex"
	// StyleIFlow 统一 iFlow Auth 类型（凭据来自订阅池，不要求手填 api_key/base_url）。
	StyleIFlow Style = "iflow"
	// StyleCodexAuths 专用于 Codex OAuth 订阅凭据（auth_files）轮询注入。
	StyleCodexAuths Style = "codex-auths"
	// StyleIFlowAuths 专用于 iFlow 订阅凭据（auth_files）轮询注入。
	StyleIFlowAuths Style = "iflow-auths"
	StyleAnthropic  Style = "anthropic"
	StyleGemini     Style = "gemini"

	// StyleOpenAIEmbeddings Embeddings：用于在日志中区分请求类型（提供商类型仍沿用 openai / gemini）
	StyleOpenAIEmbeddings Style = "openai-embeddings"
	StyleGeminiEmbeddings Style = "gemini-embeddings"
)

const (
	// BalancerLottery 按权重概率抽取，类似抽签。
	BalancerLottery = "lottery"
	// BalancerRotor 按顺序循环轮转，每次降低权重后移到队尾
	BalancerRotor = "rotor"
	// 默认策略
	BalancerDefault = BalancerLottery
)

const (
	KeyPrefix = "sk-github.com/racio/orvion-"
	KeyLength = 32
)
