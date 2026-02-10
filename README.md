# Orvion

多提供商 LLM 网关（Go + Gin），兼容 OpenAI / Anthropic / Gemini 协议，内置 WebUI 管理后台与监控能力。

## 核心能力

- 多协议统一代理：`/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`、`/v1/messages`
- 多提供商与多模型编排：同模型可绑定多个上游，支持权重与能力开关
- 内置管理后台：提供商、模型、模型关联、API Key、系统配置、日志查看
- 健康检查与限流：支持 Redis，Redis 不可用时自动降级到内存
- 支持 Codex / iFlow 官方订阅对接（OAuth） 
（由于技术原因，OAuth添加 只有本地运行时支持，若是服务器部署，需要把本地验证完生成的xxx-auths文件夹复制到挂载目录上）


## 项目结构

```text
.
├── main.go                 # 应用入口
├── router.go               # 路由注册与 WebUI 静态资源挂载
├── makefile                # 开发命令
├── Dockerfile              # 多阶段镜像构建
├── webui/                  # 前端工程（Vite + React）
├── handler/                # HTTP 处理层
├── service/                # 业务逻辑层
├── models/                 # 数据模型与数据库初始化
├── providers/              # 各上游提供商适配
└── middleware/             # 鉴权与中间件
```

## 环境要求

- Go `1.25+`
- Node.js `20+`（用于构建前端）
- `make`
- Docker（可选）

## 开发命令（按你给的流程）

| 场景 | 命令 | 说明 |
| --- | --- | --- |
| mod 依赖加载 | `make tidy` | 等价 `go mod tidy` |
| 启动前端 | `make webui` | 执行 `cd webui && npm install && npm run build`，生成 `webui/dist` |
| 启动后端 | `make run` | 执行 `go run .` |
| 一键启动 | `make all` | 顺序执行 `make webui && make run` |

推荐开发顺序：

```bash
make tidy
make webui
make run
```

访问地址：

- WebUI：`http://127.0.0.1:7070/`
- 健康检查：`http://127.0.0.1:7070/health`
- 健康详情：`http://127.0.0.1:7070/health/detail`

## Docker 运行

```bash
docker run -d \
  --name orvion \
  --restart unless-stopped \
  -p 7070:7070 \
  -e TOKEN=your_token_here \
  -v /usr/orvion/data:/orvion/data \
  --pull always \
  ghcr.io/raciott/orvion:latest
```
(若是二次开发，请把镜像修改为 `ghcr.io/{你的github名字}/orvion:latest`)

## 本地打包运行

先打包前端，再编译后端：

```bash
make webui
go build -o orvion .
```

运行二进制：

Linux / macOS：

```bash
TOKEN=your_token_here ./orvion
```

Windows PowerShell：

```powershell
cmd /C "set TOKEN=your_token_here && .\orvion.exe"
```

## 常用环境变量

| 变量名 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKEN` | 空 | 管理接口与代理接口鉴权 Token |
| `DATABASE_DSN` | `./data/llmio.db` | SQLite 或 PostgreSQL 连接串 |
| `REDIS_URL` | 空 | Redis 地址（不配则使用内存限流） |
| `LLMIO_SERVER_PORT` | `7070` | 服务端口 |
| `LOG_LEVEL` | `info` | 日志级别：`debug/info/warn/error` |

可参考 `.env.example` 配置本地运行环境。

## 常用接口

- `GET /health`
- `GET /health/detail`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`
- `POST /v1/messages`

示例：

```bash
curl -sS -X POST "http://127.0.0.1:7070/v1/chat/completions" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"gpt-4.1\",\"input\":\"你好\"}"
```

## License

MIT
