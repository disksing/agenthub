# AgentHub

[English](README.md)

AgentHub 是一个本地 Agent 启动器与 Session 中枢。一个 Go daemon 统一管理本机 Codex、Kimi、Pi/Grok 和 OpenCode，Web UI 与 CLI 都通过同一套 HTTP API 和 SSE 事件流工作。

## 能力

- 默认仅监听 loopback，可显式配置局域网/通配/IPv6 地址；无账号、token 或 API 鉴权。
- 独立的 provider 和 agent 配置。
- 创建 Session 时始终显式选择一个 Agent，没有隐式路由或回退。
- 真实 Provider Adapter：
  - Codex app-server
  - Kimi / OpenCode ACP v1
  - Pi JSONL RPC，包括 Kimi K3 与 Grok 等模型
- Session 创建、聊天、steer、interrupt、stop、resume、archive 和 approval。
- daemon 重启后按需恢复 provider 原生 session/thread。
- 同源 Web UI：Session 列表、实时聊天、状态、审批、停止，以及用于管理 provider 和 agent 的结构化设置界面。
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

### 监听地址

默认只监听 loopback，`agenthub serve` 等价于 `agenthub serve --addr 127.0.0.1:4646`。需要让局域网内其他设备访问时，可以用 `--addr host:port` 显式选择本机地址：

```bash
agenthub serve --addr 192.168.2.150:4646   # 具体局域网 IPv4
agenthub serve --addr 0.0.0.0:4646         # 所有 IPv4 接口（通配）
agenthub serve --addr '[::]:4646'          # 所有 IPv6 接口（通配）
agenthub serve --addr '[::1]:4646'         # 仅 IPv6 loopback
agenthub serve --addr myhost.local:4646    # 解析到本机接口的主机名/域名
```

主机名必须能解析到本机网络接口或 loopback。无法解析的主机名、非本机接口的地址、错误的格式和非法端口都会在启动时直接报错，不会静默回退到其他地址。IPv6 地址必须用方括号括起来。

> **安全警告**：AgentHub 没有账号、token 或 API 鉴权。监听非 loopback 或通配地址时，任何能访问该地址的设备都可以完全控制 daemon（运行 Agent、修改 Session 和配置）。只在可信网络中使用，不要直接暴露到公网。启动日志会打印同样的警告。

非 loopback 监听时，本机 CLI 仍通过 `server.json` 中的 loopback endpoint 自动发现 daemon。浏览器写请求仍要求 `Origin` 与请求 `Host` 一致，且 `Host` 必须是本机接口地址或本机主机名（防止 DNS rebinding），不接受任意 Origin。

开发时可分别运行：

```bash
go run ./cmd/agenthub serve
cd frontend && npm run dev
```

Vite 会把 `/v1` 代理到默认 daemon 端口。

## CLI

CLI 提供分层帮助：`agenthub help` 输出总览和概念导读（Provider、Agent、Session、Turn、Approval、事件），`agenthub help <command>`（或 `agenthub help session <subcommand>`）输出单条命令的用法、选项、默认值和示例，`agenthub <command> --help` 效果相同。未知命令和错误参数会以非零状态退出，并提示对应的帮助入口。

```bash
agenthub help
agenthub help session approve
agenthub serve --help

agenthub status
agenthub agents

agenthub run --agent pi-kimi --cwd . "检查测试失败原因"
agenthub run --agent codex-default --cwd . "实现这个功能并运行测试"

agenthub chat --agent gpt-5-6-sol --cwd .
agenthub session create --agent pi-kimi --title "bug hunt"
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
  ]
}
```

Provider 封装一个本地 Agent 运行时或协议；Agent 引用一个 Provider 并保存具体启动参数。每个 Session 都用显式 Agent ID 创建（`POST /v1/sessions` 要求 `agentId`，CLI 要求 `--agent`）；未知、缺失或 Provider 被禁用的 Agent 会直接返回明确错误，不会被路由到其他 Agent。

推荐使用 Web UI 的 **Settings** 界面编辑配置：它为 provider 和 agent 提供结构化、带校验的表单，并展示 provider 命令可用性探测结果。所有修改都通过 daemon API（`PUT /v1/config`）提交，daemon 仍是配置文件的唯一写入者，无需手动编辑 JSON。

### 旧配置迁移

早期版本支持 agent profile、基于 tag 的路由和 `defaultChatAgentId` 回退，这些能力已移除：Session 现在始终显式指定 Agent。仍含有遗留 `agentProfiles` 或 `defaultChatAgentId` 键的配置文件可以继续使用——这些键在读取时被忽略，daemon 启动时会把配置文件一次性重写为不含这些键的形式。Provider 和 Agent 数据原样保留。在此变更之前记录的 Session 只要已确定 Agent 就仍可查看和恢复；从未启动过（因而没有 Agent）的旧 Session 会返回明确错误，而不会猜测到某个已配置的 Agent 上。

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

无鉴权模式只适合本机和可信网络：默认仅监听 loopback，不发送 CORS 许可，拒绝跨 origin 的浏览器写请求，并校验请求 Host 必须指向本机地址。

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
