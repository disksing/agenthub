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
- 同源 Web UI：Session 列表、实时聊天、状态、审批、停止，以及包含四个内置 Provider 开关、结构化 Agent 表单和按 Provider 加载的模型下拉框的设置界面。
- 浮动伙伴：展示由 OnWatch 提供的 Provider 配额，以及由 AgentHub 自身事件聚合出的实时 Session 活动，可选 Web Audio 提示音。
- Provider 模型枚举：每个内置 Provider 都能通过各自的官方接口报告当前可用模型，统一为一个只读 API。
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

agenthub run --agent "Kimi K3" --cwd . "检查测试失败原因"
agenthub run --agent "Codex" --cwd . "实现这个功能并运行测试"

agenthub chat --agent "Codex GPT" --cwd .
agenthub session create --agent "Kimi K3" --title "bug hunt"
agenthub session attach <session-id>
agenthub session list
agenthub session list --archived
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
      "name": "Kimi K3",
      "providerId": "pi",
      "options": { "mode": "build", "model": "kimi-coding/k3" }
    }
  ],
  "onWatch": {
    "enabled": false,
    "serverUrl": "http://127.0.0.1:9211",
    "authMode": "trusted_proxy",
    "username": "admin",
    "password": "",
    "refreshIntervalSeconds": 60
  },
  "companion": {
    "showActivity": true,
    "enableBeeping": true,
    "beepVolume": 0.28,
    "completionSound": "completed-voice"
  }
}
```

浮动伙伴可以拖到视口内任意位置；归一化位置保存在浏览器本地存储中，刷新后仍会恢复，并会在视口缩放后保持可见。打开时，它会根据当前位置选择向上/向下展开及向左/向右对齐，确保卡片留在屏幕内。展开卡片还可以从外侧角调整宽高；保存的尺寸会按当前视口自动夹紧，内部控制项、波形、配额分栏和滚动布局会响应窄、矮或较宽的尺寸。标题栏可在新标签页打开 `/beeper`；独立监控页以深色内容填满整个视口，竖屏时 Provider 配额保持单列，并继续提供设置入口。Activity 波形由 AgentHub 全局 Activity SSE 流驱动：每个 Session 在每个一秒活动帧中只产生一个波峰，不受底层 event 数量影响；同一帧内多个 Session 的波峰与活动蜂鸣共用均匀分布的时间点，从右侧进入后平滑向左滚动。活跃 Session 标签一行一个，每次有新活动时变亮，并在 10 秒内恢复初始颜色。完成提示音使用内置的 6 个 Codex Beeper MP3。

Provider 封装一个本地 Agent 运行时或协议；Agent 引用一个 Provider 并保存具体启动参数。Agent 没有独立的 id：它的 `name` 必填（最长 80 个字符），在去除首尾空白后大小写不敏感地唯一，并且是唯一引用键——配置、API、CLI 和 Session 记录都使用它。每个 Session 都用显式 Agent 名称创建（`POST /v1/sessions` 要求 `agentName`，CLI 要求 `--agent`）；名称匹配不区分大小写，Session 记录的是配置中的规范拼写。未知、缺失或 Provider 被禁用的 Agent 会直接返回明确错误，不会被路由到其他 Agent。

重命名 Agent 是安全的：当一次配置保存把某个名称替换为唯一一个其余字段完全相同的 Agent 时，daemon 会向每个引用旧名称的活动 Session 追加 `session.agent` 事件，使这些 Session 跟随重命名。歧义重命名（存在多个字段相同的候选）会被拒绝并给出可操作错误；删除 Agent 或重命名到不存在唯一目标时，旧 Session 会以清晰的“unknown agent”错误失败，而不会猜测到其他 Agent。

推荐使用 Web UI 的 **Settings** 界面管理配置。**Providers** 区块刻意保持极简：只有四个开关用于启用或停用内置 Provider（Codex、Kimi、Grok/Pi、OpenCode），不提供 Provider 增删，也不提供命令、参数、环境变量或其他高级字段的编辑。开关只通过 `PUT /v1/config/providers/{id}` 翻转 `enabled` 标志，底层配置在停用/重新启用后完整保留；旧配置中缺失的内置 Provider 会在首次启用时由 daemon 以规范默认值创建。**Agents** 区块保留结构化、带校验的表单，Provider 命令可用性探测会区分“已启用”与“CLI 可用”。所有修改都通过 daemon API 提交，daemon 仍是配置文件的唯一写入者，无需手动编辑 JSON。

**General** 和 **Activity** 区块用于配置浮动伙伴。Provider 配额只由 daemon 从 OnWatch 拉取，经归一化和按配置间隔缓存后通过 `GET /v1/quota` 暴露；Basic Auth 密码保存在本机权限为 `0600` 的配置文件中，但所有 API 响应都会将其抹除。Session 活动只来自 AgentHub 自身持久化追加的事件，并在 `GET /v1/activity/events` 中按 Session 聚合为一秒一帧。浏览器只保持一条全局 EventSource，为每个活跃 Session 分配稳定音高，通过 Web Audio 合成可选的运行蜂鸣，并播放用户选择的本地 MP3 完成提示音。此功能不会扫描 Codex 或其他 Provider 的原生 Session 文件。

Provider 被停用后，其 Agent 会被标记为不可用（`GET /v1/agents` 中 `available: false` 并附原因），不会出现在新建 Session 的选择中；即使绕过 Web UI 直接调用 API，daemon 也会拒绝用这些 Agent 创建或恢复 Session。停用不会中断已经在运行的 Session，既有 Session 历史仍可查看。

### Per-session 启动环境

`POST /v1/sessions` 接受可选的字符串映射 `launchEnvironment`。它会覆盖 daemon 环境中的同名变量，并只传给该 Session 的 Provider 进程；Codex 还会在 `thread/start` 和 `thread/resume` 时把每项映射为 `shell_environment_policy.set.<KEY>`，ACP 和 Pi 使用合并后的进程环境。该字段存放在持久化的 `session.created` 事件中，因此 event replay、daemon 重启和 Provider resume 后仍然有效，并发 Session 之间不会串值；没有该字段的旧 Session 继续直接继承 daemon 环境。

`launchEnvironment` 会明确写入 Session 的 `events.jsonl` 和可重建的 `session.json`，也会由 Session API 返回。不要放入任何不希望持久化的凭据或其他 secret。Session 文件继续使用 `0600` 权限，但文件权限不能替代专门的 secret 存储。

### 模型枚举

每个内置 Provider 都可以通过各自的官方接口报告本机当前可用的模型——不创建 Provider Session，也不写入 Provider 配置：

- Codex：app-server 的 `model/list` 请求（按账号过滤，含展示名、默认标记和隐藏模型过滤）。
- Kimi：`kimi provider list --json`（已配置的模型注册表，含展示名）。
- Pi：`--no-session` 模式下的 RPC `get_available_models` 命令（覆盖所有已配置上游；会标记 Pi 默认使用的模型）。
- OpenCode：`opencode models --verbose`（已配置 Provider 及 OpenCode Zen 免费模型，含展示名）。

`GET /v1/providers/{id}/models` 把四方统一为 `{ "provider": {...}, "models": [{ "id", "label", "default" }] }`，其中 `id` 就是可直接写入 Agent `model` 选项的值。结果按 ID 去重、保持 Provider 原有顺序，带短期缓存（成功 5 分钟、失败 15 秒）与并发去重，并在每次配置变更（整体保存或 Provider 开关）时失效。失败按类别区分，便于客户端分别展示：`404 unknown_provider`、`409 provider_disabled`、`503 provider_unavailable`（CLI 缺失或无法启动）、`504 provider_timeout`、`502 provider_error`（上游或解析失败）；空列表是成功的 `200` 并返回 `"models": []`。该端点是只读的：不创建 Provider Session，也不修改配置。

在 Web 设置中，Agent 的 **Model** 字段是由该端点加载的下拉框，不再是自由文本输入：先选择 Provider，再选择模型。空的“Provider default”选项表示不设置 `model` 选项。已保存但当前列表中不存在的模型会保留为明确的“saved, not currently listed”选项，直到用户主动更换；加载、重试、空列表和 Provider 停用状态都会内联展示。

### 已移除的旧格式

Session 现在只使用显式 Agent 名称作为身份。Agent Profile、tag 路由、`defaultChatAgentId`、Agent `id` 字段，以及 `POST /v1/sessions` 的 `agentId` 字段均不再接受。daemon 不会重写旧配置、创建 id 映射 sidecar，也不会回放使用旧身份字段的事件 payload。升级到此版本前，请先把旧配置和 Session 数据完成一次性转换或备份。

命令发现顺序为：provider 的 `command`、`AGENTHUB_*_CLI`、`PATH`。支持：

- `AGENTHUB_CODEX_CLI`
- `AGENTHUB_OPENCODE_CLI`
- `AGENTHUB_KIMI_CLI`
- `AGENTHUB_PI_CLI`
`AGENTHUB_HOME=/path` 可把配置、数据和 runtime 状态全部隔离到一个目录，配置文件此时位于 `/path/config/config.json`，适合测试。该布局由用户显式选择，daemon 按原样读取。

## API

daemon 在 **`GET /api.md`** 提供完整的 Markdown API 参考（`text/markdown; charset=utf-8`）：覆盖全部公共端点的参数、请求与响应体、错误码、curl 示例和 SSE 事件约定。文档内嵌在二进制中，无需前端构建即可访问，并由自动化测试保证与真实注册路由同步。获取方式：

```bash
curl -s http://127.0.0.1:4646/api.md
```

主要端点：

```text
GET    /v1/health
GET    /v1/status
GET    /v1/config
PUT    /v1/config
POST   /v1/onwatch/test
GET    /v1/quota
GET    /v1/activity/events
GET    /v1/agents
GET    /v1/providers/{id}/models

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

事件端点在普通请求下返回分页 JSON，包含 exclusive `after` cursor、`nextAfter`、`hasMore` 和最新 durable cursor；带 `Accept: text/event-stream` 或 `?stream=true` 时返回 SSE，并通过 `Last-Event-ID` 使用相同的 exclusive cursor 语义。daemon 先建立订阅并捕获 high-water mark，再分页重放完整 durable backlog，最后切换到 live；subscriber overflow 会关闭流，客户端可从最后一个连续 id 重连并从 `events.jsonl` 恢复，daemon 重启后同样有效。SSE 帧使用默认 message 通道，因此未知 event envelope 也会原样传输。

### 消息来源角色

`POST /v1/sessions/{id}/messages` 接受 `role: "user"`、`"system"` 或
`"agent"`；省略 `role` 时为兼容旧客户端按 `user` 处理。可选的
`sender` 对象可携带描述性 `id`、`name` 和 `sessionId`。这些字段只表示
provenance（消息来源）：它们由调用方自报、不会认证，也不会改变权限、信任
级别或指令优先级。`assistant` 只保留给当前 Provider 产生的输出事件，入站
客户端不能伪造。

新消息统一持久化为包含原始文本、role、sender 和 `steer` 的
`message.input` 事件。历史 `message.user` 与 `message.user.steer` 事件重放
时等价为 user 消息，不改写 Session 日志。system 与 agent 消息发给 Codex、
Kimi、OpenCode 和 Pi 时仍是普通用户级文本，只额外携带 JSON 来源信封；信封
不会进入公共事件时间线，也不会显示在 Web UI 中。

示例：

```json
{"text":"请检查失败的测试。"}
{"text":"恢复排队的工作。","role":"system","sender":{"name":"Workflow Coordinator"}}
{"text":"Worker 已完成扫描。","role":"agent","sender":{"name":"Review Agent","sessionId":"ses_worker"}}
```

### 可复用 Event Timeline

[`packages/event-timeline`](packages/event-timeline/README.md) 是 API v1
canonical event 的无依赖参考投影。AgentHub Web 直接导入其 ESM 产物；无
bundler 的浏览器客户端可固定 vendoring IIFE，并调用
`AgentHubEventTimeline.buildTimeline(events)`。package 包含脱敏的一致性
fixtures 与精确 snapshots，覆盖 Provider 噪声、跨 Turn delta、reasoning、
Codex/ACP/Pi 工具、审批、终态兜底、失败、取消、未知事件和分页重放。

在 package 目录运行 `npm run build` 和 `npm test` 可复现并验证两种产物。
`dist/manifest.json` 记录版本 `1.0.0`、契约 `agenthub.api.v1`、
BSD-3-Clause 许可证、确定性构建命令，以及源码输入和生成产物的 SHA-256。
下游应固定包含该 manifest 的 AgentHub Git commit。

### 归档 Session

`DELETE /v1/sessions/{id}` 归档一个 Session：daemon 先追加一条持久化的 `session.archived` 事件，再把整个 Session 目录移动到 Session Store 的 `Archive/` 子目录（`sessions/Archive/<session-id>/`）。不删除任何数据——`session.json`、`events.jsonl` 和其他文件一起移动。

- 仅严格处于 `stopped` 且没有未完成 Turn 或待处理审批的 Session 可以归档。端点不会强制停止 Session；请先调用 `POST /v1/sessions/{id}/stop`。
- 状态码语义：`200` 归档成功（重复归档幂等成功）；`404` Session 不存在；`409 session_active` Session 仍有活动工作；`409 session_archive_conflict` 归档目标被占用；`500 session_archive_failed` 文件系统错误（数据保持完整，重试或 daemon 重启会补完移动）。
- `GET /v1/sessions` 默认不返回归档 Session；用 `?includeArchived=true` 包含它们，或用 `?archived=true` 只列出归档 Session。归档后 `GET /v1/sessions/{id}` 和事件端点仍然可读。
- 归档 Session 只读：`messages`、`resume`、`interrupt`、`stop` 和审批写入都返回 `409 session_archived`。不支持取消归档。

CLI 对应命令是 `agenthub session archive <id>`、`agenthub session list --all` 和 `agenthub session list --archived`；Web UI 提供带应用内确认框的 Archive 操作和“Archived Sessions”视图。

### Provider 启动失败

创建 Session 会同步启动 Provider：握手请求（`initialize`、`session/new` / `thread/start` 及其 resume/load 变体）必须在 2 分钟启动超时内应答。无法应答的 Provider——例如进程因操作系统挂起隐私授权弹窗而卡在读取 Session 工作目录上——会让创建请求快速失败而不是一直挂起：

- API 返回 `502 provider_start_failed`，携带 Provider 的真实错误；超时时附带可操作的提示（在 macOS 上指向“系统设置 > 隐私与安全性”弹窗，例如“下载”文件夹或完全磁盘访问权限）。Web 新建 Session 窗口会显示该信息。
- 失败 Session 保留用于诊断：记录 `provider.error`、失败闭合 open turn，并在确认进程退出后收敛到带 `stopReason: "startup_error"` 的 `stopped`；之后可查看、resume、归档或保留。

活动 Provider Turn 不设置固定的总时长上限。ACP `session/prompt` 与 Pi
`prompt`/`steer` 会等待 Provider 的真实终态，即使推理或工具执行超过 15 分钟；
用户仍可通过 interrupt 或 stop 主动结束，Provider 退出或 daemon 关闭也会解除等待。
启动握手继续保留 2 分钟上限，普通控制请求则继续使用单独的有界超时。

### Strict stopped 生命周期与崩溃恢复

`stopped` 是唯一可信的 Provider 资源释放边界。stop 请求先写入
`stopping`；只有 adapter Wait 路径与进程组探测确认 Provider 及其子进程
不能再写工作目录后，调用才返回并追加
`session.state {"state":"stopped","reason":"requested"}`。

所有退出路径使用同一终态收敛器：正常退出使用 `completed`；崩溃先记录
`provider.error`、关闭 approval 与 open turn，再使用 `provider_error`；
启动失败使用 `startup_error`；显式 stop 和 daemon 优雅关闭使用
`requested`。daemon 被 SIGKILL 后，新 daemon 使用持久化的
`provider.process.started` 证据终止遗留进程组，确定性取消 pending
approval 与 open turn，最后使用 `daemon_recovery`。

## 数据与安全

所有用户持久数据统一存放在 `$HOME/.agenthub`：

```text
~/.agenthub/
├── config.json                 （provider 与 agent 配置）
├── sessions/<session-id>/
│     session.json
│     events.jsonl
├── sessions/Archive/<session-id>/   （归档 Session，文件相同）
├── logs/                       （以服务方式安装后的 stdout/stderr 日志）
├── server.json                 （临时 daemon 端点发现）
└── server.lock                 （临时单 daemon 锁）
```

`events.jsonl` 是唯一事实来源，`session.json` 是可重建投影。写入使用 append + fsync，快照使用临时文件 + fsync + rename；当前写入中断造成的最后半行可在启动时修复。归档只是同一 Store 内的目录移动：如果 daemon 在追加归档事件和移动目录之间停止，启动时会补完移动，使物理位置始终与事件日志一致。目录默认 `0700`，敏感文件默认 `0600`。

`agenthub status`（以及 `GET /v1/status`）会报告当前生效的配置、Session Store、归档与日志路径，便于升级后确认布局。

### 数据布局

daemon 只读取统一的 `~/.agenthub` 布局。旧版本可能把 Session 存放在操作系统用户数据目录（例如 macOS 的 `~/Library/Application Support/agenthub`），把日志存放在 `~/Library/Logs/AgentHub`；这些路径不再自动读取或迁移。升级前请完成一次性、可验证的复制或导出，并保留备份；daemon 不会合并多个数据根，也不会替用户选择保留哪一侧。

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
