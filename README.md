# AgentHub

AgentHub 是一个本地 Agent 启动器与 Session 中枢。一个 Go daemon 统一管理本机 Codex、Kimi、Pi/Grok 和 OpenCode，Web UI 与 CLI 都通过同一套 HTTP API 和 SSE 事件流工作。

## 能力

- 仅监听 loopback；无账号、token 或 API 鉴权。
- 独立的 provider、agent 和 agent profile 配置。
- Agent Profile 显式选择或按 profile key/tag 自动路由。
- 真实 Provider Adapter：
  - Codex app-server
  - Kimi / OpenCode ACP v1
  - Pi JSONL RPC，包括 Kimi K3 与 Grok 等模型
- Session 创建、聊天、steer、interrupt、stop、resume、archive 和 approval。
- daemon 重启后按需恢复 provider 原生 session/thread。
- 同源 Web UI：Session 列表、实时聊天、状态、审批、停止和 JSON 配置编辑。
- CLI：一次性运行、交互聊天、attach、事件查询和 Session 管理。
- 每个 Session 只保存 `session.json` 与连续的 `events.jsonl`；Turn 和 Approval 都是事件，不建立独立文件。

## 构建与启动

需要 Go 1.24+ 和 Node.js。Web UI 位于 `frontend/`，构建后由 daemon 同源提供：

```bash
cd frontend
npm install
npm run build
cd ..
go build -o agenthub ./cmd/agenthub
./agenthub serve
```

打开 <http://127.0.0.1:4646>。

开发时可分别运行：

```bash
go run ./cmd/agenthub serve
cd frontend && npm run dev
```

Vite 会把 `/v1` 代理到默认 daemon 端口。

## CLI

```bash
agenthub status
agenthub agents

agenthub run --tag fast --cwd . "检查测试失败原因"
agenthub run --agent pi-kimi --cwd . "实现这个功能并运行测试"

agenthub chat --agent gpt-5-6-sol --cwd .
agenthub session attach <session-id>
agenthub session list
agenthub session show <session-id>
agenthub session events <session-id>
agenthub session resume <session-id>
agenthub session interrupt <session-id>
agenthub session stop <session-id>
agenthub session approve --decision accept <session-id> <approval-id>
agenthub session archive <session-id>
```

交互聊天支持 `/interrupt`、`/stop` 和 `/quit`。CLI 自动从 daemon 的 `server.json` 发现 endpoint；也可设置 `AGENTHUB_ENDPOINT`。

## 配置

默认配置文件是：

```text
$HOME/.agenthub/config.json
```

首次启动时，如果配置不存在，AgentHub 会直接生成自己的最小默认配置，不读取或迁移其他程序的配置。配置结构：

```json
{
  "version": 1,
  "defaultChatAgentId": "pi-kimi",
  "agentProviders": [
    { "id": "codex", "name": "Codex app-server", "type": "codex", "enabled": true },
    { "id": "kimi", "name": "Kimi Code", "type": "kimi", "enabled": true },
    { "id": "pi", "name": "Pi Coding Agent", "type": "pi", "enabled": true }
  ],
  "agents": [
    {
      "id": "pi-kimi",
      "name": "pi-kimi",
      "providerId": "pi",
      "options": { "mode": "build", "model": "kimi-coding/k3" }
    }
  ],
  "agentProfiles": [
    { "key": "kimi", "description": "kimi k3", "agentId": "pi-kimi" }
  ]
}
```

命令发现顺序为：provider 的 `command`、`AGENTHUB_*_CLI`、`PATH`。支持：

- `AGENTHUB_CODEX_CLI`
- `AGENTHUB_OPENCODE_CLI`
- `AGENTHUB_KIMI_CLI`
- `AGENTHUB_PI_CLI`
`AGENTHUB_HOME=/path` 可把配置、数据和 runtime 状态全部隔离到一个目录，配置文件此时位于 `/path/config/config.json`，适合测试。

## API

主要端点：

```text
GET    /v1/health
GET    /v1/status
GET    /v1/config
PUT    /v1/config
GET    /v1/agents

POST   /v1/sessions
GET    /v1/sessions
GET    /v1/sessions/{id}
DELETE /v1/sessions/{id}
POST   /v1/sessions/{id}/messages
POST   /v1/sessions/{id}/resume
POST   /v1/sessions/{id}/interrupt
POST   /v1/sessions/{id}/stop
POST   /v1/sessions/{id}/approvals/{approvalId}
GET    /v1/sessions/{id}/events
```

事件端点在普通请求下返回 JSON；带 `Accept: text/event-stream` 或 `?stream=true` 时返回 SSE，并支持 `Last-Event-ID`。

## 数据与安全

配置默认位于 `$HOME/.agenthub/config.json`；Session 数据与运行状态仍遵循操作系统用户数据目录。每个 Session：

```text
sessions/<session-id>/
  session.json
  events.jsonl
```

`events.jsonl` 是唯一事实来源，`session.json` 是可重建投影。写入使用 append + fsync，快照使用临时文件 + fsync + rename；启动时可修复被截断的日志尾行。

无鉴权模式只适用于本机：daemon 拒绝非 loopback 地址，不发送 CORS 许可，并拒绝跨 origin 的浏览器写请求。

## 验证

```bash
go test -race ./...
go vet ./...
cd frontend
npm run build
npm run test:sites
```

实现还经过本机真实联调：Codex app-server、Kimi ACP、Pi/Kimi K3、Pi/Grok、Codex 原生 thread 重启恢复，以及 Kimi 创建并写入工作区文件。

## 许可证

本项目采用 [BSD 3-Clause License](LICENSE)（New BSD License / Revised BSD License）发布。
