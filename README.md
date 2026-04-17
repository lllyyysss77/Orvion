# Orvion

Orvion 是一个多提供商 LLM 网关（Go + Gin），提供 OpenAI/Anthropic 兼容接口、内置 WebUI 管理台、日志与健康监控能力。

## 功能概览

- 统一协议入口：`/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`、`/v1/rerank`、`/v1/images/*`、`/v1/videos`、`/v1/messages`
- 多提供商编排：同一逻辑模型可绑定多个上游，支持权重与负载均衡策略
- 熔断与自恢复：连续错误自动下线模型关联，冷却后自动恢复
- 限流控制：支持按 API Key 的 RPM 控制（内存实现）
- 管理后台：提供商、模型、模型关联、Auth Key、配置、日志、健康状态、测试场
- 可观测性：请求日志、系统日志、统计指标、健康检查接口
- Telegram 能力：熔断告警推送 + TG 命令对话（`/status`、`/model`、`/help`）

## 技术栈

- 后端：Go 1.25 + Gin + GORM
- 数据库：SQLite（默认）/ PostgreSQL
- 前端：React + Vite + TypeScript（构建产物嵌入后端）

## 快速开始

### 1) 环境要求

- Go `1.25+`
- Node.js `20+`
- `make`

### 2) 本地运行（推荐）

```bash
make tidy
make webui
make run
```

说明：

- `make webui` 会构建 `webui/dist`
- 后端通过 `go:embed` 挂载 `webui/dist`，未构建前端时可能无法通过编译

默认访问地址：

- WebUI：`http://127.0.0.1:7070/`
- 健康检查：`http://127.0.0.1:7070/health`
- 健康详情：`http://127.0.0.1:7070/health/detail`

### 3) 本地编译运行

```bash
make all
```

## Docker 运行

```bash
docker run -d \
  --name orvion \
  --restart unless-stopped \
  --network host \
  -p 7070:7070 \
  -e DATABASE_DSN="data/llmio.db" \
  -e TOKEN="xxxxxxxxx" \
  -e TZ="Asia/Shanghai" \
  -v xxxxxxxxx/orvion:/orvion/data \
  --pull always \
  ghcr.io/raciott/orvion:latest

```

如你 fork 后自建镜像，请改为 `ghcr.io/<你的仓库路径>:<tag>`。

## 配置说明

应用启动时会自动加载项目根目录 `.env`（若存在），可参考 `.env.example`。

### 核心环境变量

- `TOKEN`：管理 API 与代理接口的管理员令牌；为空时将不校验管理员接口
- `DATABASE_DSN`：数据库连接串；默认 SQLite `./data/llmio.db`
- `ORVION_SERVER_PORT`：服务端口，默认 `7070`
- `LOG_LEVEL`：日志级别（`debug/info/warn/error`）
- `LOG_FILE`：系统日志文件路径，默认 `orvion.log`
- `ORVION_SHUTDOWN_TIMEOUT_SECONDS`：优雅停机超时秒数，默认 `10`
- `TRUSTED_PROXIES`：可信代理列表，逗号分隔
- `GEMINI_COMPAT_ENABLED`：Gemini 兼容降级开关，默认开启

### Telegram（可选）

以下变量用于熔断告警与 TG 命令对话（也可在系统配置中写入 `breaker_alert_tg` 覆盖）：

- `BREAKER_ALERT_TG_BOT_TOKEN`
- `BREAKER_ALERT_TG_CHAT_ID`
- `BREAKER_ALERT_TG_API_BASE`（默认 `https://api.telegram.org`）
- `BREAKER_ALERT_TG_PROXY_URL`

## 认证与鉴权

- `/api/*`（管理接口）：
  - 当 `TOKEN` 非空时，需 `Authorization: Bearer <TOKEN>`
  - 当 `TOKEN` 为空时，不启用该鉴权
- `/v1/*`（模型代理接口）：
  - 支持管理员 `TOKEN` 或 Auth Key
  - OpenAI 兼容接口走 `Authorization: Bearer <key>`
  - Anthropic 兼容接口优先 `x-api-key`，也兼容 `Authorization: Bearer <key>`

## 主要接口

### 公共健康接口（无需认证）

- `GET /health`
- `GET /health/live`
- `GET /health/ready`
- `GET /health/detail`
- 兼容路由：`/healthz`、`/livez`、`/readyz`

### 统一代理接口（`/v1`）

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`
- `POST /v1/rerank`
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- `POST /v1/videos`
- `POST /v1/messages`
- `POST /v1/messages/count_tokens`

### 管理接口（`/api`）

- 版本与系统：`/api/version`、`/api/system-logs`、`/api/logs`
- 资源管理：`/api/providers`、`/api/models`、`/api/model-providers`、`/api/auth-keys`
- 监控指标：`/api/metrics/*`
- 配置操作：`/api/config/:key`
- 限流与测试：`/api/limiter/*`、`/api/test/*`

## 调用示例

### OpenAI 兼容聊天

```bash
curl -sS "http://127.0.0.1:7070/v1/chat/completions" \
  -H "Authorization: Bearer <TOKEN_OR_AUTH_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role":"user","content":"你好"}],
    "stream": false
  }'
```

### Anthropic 兼容消息

```bash
curl -sS "http://127.0.0.1:7070/v1/messages" \
  -H "x-api-key: <TOKEN_OR_AUTH_KEY>" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-latest",
    "max_tokens": 128,
    "messages": [{"role":"user","content":"你好"}]
  }'
```

## 版本号与镜像版本

- 运行时版本来自 `consts.Version`，默认值为 `dev`
- 可在构建时注入：

```bash
go build -ldflags "-X github.com/racio/orvion/consts.Version=v1.2.3" -o orvion .
```

- Docker 构建注入：

```bash
docker build --build-arg VERSION=v1.2.3 -t orvion:v1.2.3 .
```

若使用仓库内置 GitHub Actions（`.github/workflows/docker-image.yml`），会自动计算版本并将 `VERSION` 传入镜像构建。

## 后台任务

应用启动后会自动运行以下后台任务：

- 模型价格自动同步
- 系统日志自动清理
- 模型关联自动恢复
- Telegram 命令机器人轮询

## 项目结构

```text
.
├── main.go                 # 启动入口与后台任务
├── router.go               # 路由与 WebUI 挂载
├── handler/                # HTTP 处理层
├── service/                # 核心业务逻辑
├── providers/              # 上游协议与请求适配
├── models/                 # 数据模型与数据库初始化
├── middleware/             # 鉴权中间件
├── webui/                  # React 前端工程
├── Dockerfile              # 多阶段镜像构建
└── .github/workflows/      # CI/CD（镜像构建与发布）
```

## License

MIT
