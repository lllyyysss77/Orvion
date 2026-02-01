# Orvion (llmio)

多提供商 LLM 网关（Go + Gin），兼容 OpenAI / Anthropic / Gemini 协议，内置 WebUI 管理与监控。

---

## 特性

- 多协议代理：OpenAI `/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`，Anthropic `/v1/messages`，Gemini 原生接口
- 多提供商/多模型：同一模型可挂载多个提供商模型，支持权重与能力标记
- 统一管理：提供商、模型、模型关联、API Key、系统配置
- 请求日志：耗时/Token/费用统计与详情查看
- 健康监控：提供商与模型健康状态可视化
- 价格同步：按配置定时同步模型价格（仅同步已配置模型）
- 运行时稳定：Redis 不可用时自动降级为内存限流

---

## 快速开始

### 运行

```bash
go run .
```

默认地址：
- WebUI：`http://127.0.0.1:7070/`
- 健康检查：`http://127.0.0.1:7070/health`
- 健康详情：`http://127.0.0.1:7070/health/detail`

### 默认数据库

默认使用 SQLite，自动创建缺失表（仅缺表创建，不会修改已有表结构）：

```
./data/llmio.db
```

如需使用 PostgreSQL，设置 `DATABASE_DSN` 即可。

---

## 环境变量

必需（建议设置）：
- `TOKEN`：管理端与代理接口鉴权 Token（可为空；不推荐暴露公网）

可选：
- `DATABASE_DSN`：数据库连接串  
  - SQLite：`data/llmio.db` / `sqlite://data/llmio.db` / `file:data/llmio.db?cache=shared`
  - PostgreSQL：`postgres://user:pass@host:5432/llmio?sslmode=disable`
- `REDIS_URL`：Redis URL（RPM/Token 锁；不配置则使用内存）
- `LLMIO_SERVER_PORT`：服务端口（默认 `7070`）
- `TRUSTED_PROXIES`：可信代理 IP/CIDR（反代部署时获取真实客户端 IP）
- `LOG_LEVEL`：日志级别（debug/info/warn/error，默认 info）

示例：
```env
TOKEN=your_token_here
LOG_LEVEL=info
DATABASE_DSN=sqlite://data/llmio.db
REDIS_URL=redis://localhost:6379/0
```

---

## Docker 部署

构建镜像：
```bash
docker build -t llmio:latest .
```

运行：
```bash
docker run --rm -p 7070:7070 \
  -e "TOKEN=your_token_here" \
  -e "DATABASE_DSN=sqlite://data/llmio.db" \
  -e "REDIS_URL=redis://host.docker.internal:6379/0" \
  llmio:latest
```

---

## GitHub Actions 自动镜像

推送到 `main` 分支时会自动构建并推送到 GHCR。  
版本规则：从 `0.1.0` 起，每次推送自动递增到 `0.2.0 / 0.3.0 ...`

镜像示例：
- `ghcr.io/<owner>/<repo>:0.x.0`
- `ghcr.io/<owner>/<repo>:latest`

---

## 代理接口

OpenAI 兼容：
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/embeddings`

示例（/v1/responses）：
```bash
curl -sS -X POST "http://127.0.0.1:7070/v1/responses" \
  -H "Authorization: Bearer <TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4.1","input":"你好"}'
```

---

## 管理功能

WebUI 提供：
- 提供商管理（类型/配置/控制台）
- 模型管理（模型、价格显示）
- 模型-提供商关联（权重、能力开关、Header 透传）
- API Key 管理与用量统计
- 请求日志与详细追踪
- 健康监控与模型健康详情
- 系统配置（全局代理 IP、价格同步）

---

## 开发与构建

前端开发：
```bash
cd webui
npm install
npm run dev
```

前端构建：
```bash
cd webui
npm run build
```

后端构建：
```bash
go build -o llmio .
```

---

## 运行说明

- 自动建表仅在表不存在时执行；不会修改已有表结构
- Redis 不可用时会自动降级为内存限流
- 生产环境建议设置 `LOG_LEVEL=info` 或 `warn`

---

## License

MIT
