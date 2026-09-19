# airway-im-sdk-ts

[Airway IM](https://github.com/daqing/airway-im-plugin) 的 JS/TS SDK，覆盖
微信小程序与浏览器（Vue / React / 普通网页）：把后端的 REST API 与 WebSocket
网关协议封装成一套开箱即用的 TypeScript 接口，客户端无需自己实现凭证、
网关首帧认证、心跳、断线重连和序列补同步等协议代码。

零运行时依赖，输出 CommonJS + ESM 双格式（`dist/cjs` / `dist/esm`），同时兼容
微信开发者工具「构建 npm」、Taro / uni-app 以及 Vue / React 等浏览器框架。

## 功能

- **REST 全量封装** — 个人资料、会话（单聊 get-or-create / 群聊 / 成员管理）、
  消息（历史、发送、幂等重试）、文件上传，全部强类型并自动解 `{code,data,message}` 信封。
- **实时网关** — 自动完成 `{"cmd":"auth"}` 首帧认证、应用层心跳（默认 25s）、
  指数退避断线重连（0.5s→10s）、按 `event_id` 去重。
- **同步引擎** — 维护每个会话的 `sequence` 游标：收到 `message.created` 事件自动
  按 `after_sequence` 补洞补齐，乱序 / 重复 / 掉线期间的消息最终都会**有序、不重、
  不漏**地通过 `message` 事件送达；游标可持久化到本地存储，冷启动只拉增量。
- **凭证续期** — 凭证过期（HTTP 401/10001 或网关认证失败）时自动回调
  `getCredential` 换新凭证并恢复，业务代码无感。
- **小程序友好** — 默认适配器基于 `wx.request` / `wx.connectSocket` /
  `wx.uploadFile` / `wx.*StorageSync`，并自动在 `wx.onAppShow` 时恢复连接
  （小程序切后台会杀掉 socket）。
- **浏览器支持** — 内置 `browserAdapter`（fetch / WebSocket / FormData /
  localStorage），Vue / React / 普通网页直接可用；标签页切回前台自动重连。

## 安装

```bash
npm install airway-im-sdk-ts
```

微信开发者工具：菜单「工具 → 构建 npm」后即可 `import`。
Taro / uni-app 直接 import 即可（优先使用 ESM 产物）。

## 快速开始

### 1. 由你自己的业务后端获取并下发凭证

SDK 不含登录逻辑，也不负责身份认证。用户先在你的平台完成自己的登录
（账号密码、手机验证码、微信 `code2session` 等）；登录通过后，**你（第三方
平台）自己的服务器后端**（PHP、Java 等任意语言）从自己的用户表取出
`(name, uuid)`，向 Airway 项目的 IM 服务以**服务端对服务端**方式请求签发凭证
——调用内部签发接口 `POST /internal/v1/credentials`（携带
`X-IM-Internal-Secret`），再把返回的凭证连同你自己的会话 token 一起在登录
响应里下发给小程序（24 小时有效，过期前重新签发）。

两个必须分清的点：

- 凭证永远由**你的后端**获取并下发，客户端从不向 IM 服务器索取凭证：IM
  的公开 API（Airway 项目的 `:1905`）只验签、从不给客户端签发凭证。签发只
  发生在内部接口 `POST /internal/v1/credentials` 上，受 `IM_INTERNAL_SECRET`
  保护，仅限服务端之间调用。该接口默认只监听 `127.0.0.1:1906`：你的后端
  与 Airway 项目同机部署时可直接调用；不同机时两者必须处在同一个内网（同
  VPC / 机房，或 VPN、专线打通），由 Airway 项目方把 `IM_INTERNAL_ADDR`
  绑定到内网网卡后，经内网地址调用——不向公网暴露。
- 签名密钥 `IM_AUTH_SECRET` 只保存在 Airway 项目的 IM 服务端，你的后端只需要
  `IM_INTERNAL_SECRET`。`(name, uuid)` 也不需要、不应该由客户端发送——
  它们本来就在你的数据库里。客户端没有 secret，只持有并出示签好的凭证
  （REST 用 `Authorization: Bearer`，WebSocket 用首条 `auth` 命令）。

**前提：你的平台必须有自己的服务器后端。** 纯前端、无服务器的小程序无法
安全接入——客户端没有任何安全途径获取凭证，也没有地方存放
`IM_INTERNAL_SECRET`；能直接获取凭证的一方，就能冒充任意用户。

其他语言与完整规则见
[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)。

### 2. 小程序内初始化并收发消息

```ts
import { createIM } from "airway-im-sdk-ts";

const im = createIM({
  apiUrl: "https://im.example.com",   // IM 后端（:1905），必须是 https
  wsUrl: "wss://im.example.com",      // WebSocket 网关（:1910），必须是 wss
  credential: wx.getStorageSync("im-credential"),
  getCredential: () =>                // 凭证失效时自动调用
    fetchNewCredentialFromYourBackend(),
});

// 监听消息：有序、去重、自动补洞（含掉线期间错过的）
im.on("message", (msg, source) => {
  console.log(`[${source}]`, msg.sender.nickname, msg.content);
});
im.on("status", (s) => console.log("connection:", s));

im.connect();

// 首次打开某个会话：拉历史并开始跟踪该会话的实时同步
const { id } = await im.createDirect(otherUserUuid);
const history = await im.history(id);   // 返回按 sequence 升序的全部消息

// 发送（SDK 自动生成 Idempotency-Key，网络失败自动用同一 key 重试，不会重发）
const sent = await im.sendMessage(id, "你好", { contentType: "text/plain" });
// 或一步到位：get-or-create 单聊会话后直接发送
await im.sendDirectMessage(otherUserUuid, "你好");
```

### 3. 页面生命周期建议

小程序切后台后 socket 会被系统断开，SDK 默认（通过 `wx.onAppShow`）在回到前台
时自动重连并补同步；若你自管生命周期，可在 `App` 中：

```ts
App({
  onShow() { im.connect(); },     // connect() 幂等
  onHide() { /* 可保持连接，SDK 会自动恢复 */ },
});
```

## API 一览

### `createIM(options)`

| 选项 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `apiUrl` | ✓ | — | IM 后端地址（:1905），生产必须 https |
| `wsUrl` | | — | 网关地址（:1910）；不传则只用 REST，不连实时 |
| `credential` | ✓ | — | 业务后端签发的用户凭证 |
| `getCredential` | | — | `() => Promise<string>`，凭证失效时换新 |
| `adapter` | | 微信适配器 | 平台适配器；浏览器传 `browserAdapter()`（见下文） |
| `timeoutMs` | | `15000` | REST 请求超时 |
| `pingIntervalMs` | | `25000` | 应用层心跳间隔，`0` 关闭 |
| `persistSequences` | | `true` | 持久化各会话 sequence 游标 |
| `autoConnect` | | `false` | 创建后立即连网关 |

### REST 方法（`im.*`）

| 方法 | 对应接口 |
| --- | --- |
| `im.me()` | `GET /api/v1/me` |
| `im.listGroups()` | `GET /api/v1/conversations?type=group` |
| `im.createConversation({kind, memberUuids, title?})` | `POST /api/v1/conversations` |
| `im.createDirect(otherUserUuid)` | 同上（direct get-or-create） |
| `im.getDirectConversation(otherUserUuid)` | `GET /api/v1/conversations/direct/:user_uuid`（没有则返回 null） |
| `im.createGroup(title, memberUuids)` | `POST /api/v1/group` |
| `im.getConversation(uuid)` | `GET /api/v1/conversations/:uuid` |
| `im.addMembers(conversationId, memberUuids)` | `POST .../members` |
| `im.removeMember(conversationId, userUuid)` | `DELETE .../members/:user_uuid` |
| `im.history(conversationId, {fromSequence?, limit?})` | 拉历史 + 开始跟踪同步 |
| `im.listMessages(conversationId, {afterSequence?, limit?})` | `GET .../messages`（原始分页） |
| `im.sendMessage(conversationId, content, opts?)` | `POST /api/v1/messages` |
| `im.sendDirectMessage(otherUserUuid, content, opts?)` | Get-or-create 单聊会话后走 `POST /api/v1/messages` |
| `im.uploadFile(filePath \| File, dir?)` | `POST /api/v1/storage` |
| `im.storageUrl(key)` | 文件下载地址（配 `<image>` / `wx.downloadFile`） |

`sendMessage` 的 `opts`：`contentType`（`text/markdown` 默认 / `text/plain`）、
`idempotencyKey`（默认自动生成）、`retries`（网络失败重试次数，默认 1）。

### Message 对象（`ChatMessage`）

`sendMessage`、`sendDirectMessage`、`listMessages`、`history` 以及实时
`message` 事件携带的都是同一个 `ChatMessage` 结构：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string，26 位 ULID | 服务端生成的全局唯一消息 ID；去重的稳定键。 |
| `conversation_id` | string，26 位 ULID | 消息所属会话；据此路由到对应的聊天窗口。 |
| `sender.uuid` | string | 作者的稳定身份 uuid（API 与事件中的身份一律用 uuid）。 |
| `sender.username` | string | 宿主应用分配的账号名，账号生命周期内稳定。 |
| `sender.nickname` | string \| null | 显示昵称；为 null 时回退用 `username` 渲染。 |
| `sender.avatar_url` | string \| null | 头像 URL；为 null 时渲染占位图。 |
| `content` | string | 消息正文，1–32768 个 UTF-8 字节，服务端已把 CRLF 归一为 LF。被审核屏蔽的消息读回字面量 `***`。 |
| `content_type` | string | `text/markdown`（默认）或 `text/plain`——决定 `content` 的渲染方式。 |
| `created_at` | string，RFC 3339 UTC | 服务端提交时间戳；展示时换算为用户本地时区。 |
| `sequence` | number ≥ 1 | 消息在会话内的位置，提交时分配。`(conversation_id, sequence)` 是排序依据；把最后见到的值作为 `afterSequence` 传入即可翻历史。 |

排序用 `sequence`，不要用 `created_at`。同一 `idempotencyKey` 的重试返回原始
消息（`id`、`sequence` 都不变）；实时事件的去重与补洞 SDK 已自动处理。
`sender` 反映作者当前资料，不是发送时刻的快照。

### 实时事件（`im.on(name, handler)`）

| 事件 | 载荷 | 说明 |
| --- | --- | --- |
| `message` | `(msg, "history"\|"realtime")` | 新消息，有序、去重、自动补洞 |
| `message.updated` | `(msg)` | 消息被审核屏蔽，content 已是 `***`，用替换渲染 |
| `members.added` | `{conversationId, addedUserUuids, event}` | 群成员加入（含被拉人自己） |
| `members.removed` | `{conversationId, removedUserUuid, event}` | 群成员被移出（含被踢者本人收到通知） |
| `status` | `ConnectionStatus` | `connecting / authenticating / online / reconnecting / offline / closed` |
| `error` | `Error` | 网关认证失败、凭证续期失败等 |
| `event` | 原始 `GatewayEvent` | 所有网关帧；未 `history()` 过的会话消息也在这里 |

连接控制：`im.connect()`（幂等）/ `im.disconnect()` / `im.isOnline` /
`im.connectionStatus` / `im.setCredential(cred)` / `im.lastSequence(conversationId)` /
`im.forgetConversation(conversationId)`（如被踢出群后丢弃该会话的同步游标）。

### 错误处理

所有 REST 错误抛出 `IMError`（`err.code` 信封业务码、`err.status` HTTP 状态码，
传输失败时 `status === 0`）。常用判断：

```ts
import { IMError, ErrorCode } from "airway-im-sdk-ts";

try {
  await im.sendMessage(id, "hi");
} catch (err) {
  if (err instanceof IMError) {
    if (err.isAuthError) /* 10001：凭证无效/过期，等待自动续期或重新登录 */;
    else if (err.code === ErrorCode.PermissionDenied) /* 10005：不是会话成员 */;
    else if (err.code === ErrorCode.ConversationNotFound) /* 11001 */;
  }
}
```

完整错误码：`10000` 内部错误、`10001` 凭证无效、`10003` 请求非法、
`10005` 无权限、`11001` 会话不存在、`11002` 幂等 key 复用且请求不同。

## 消息可靠性模型

后端保证每个会话内 `sequence` 单调递增，投递是 **at-least-once**。SDK 的同步引擎
负责：按 `event_id` / `message_id` 去重、按 `sequence` 排序、发现缺口时用
`after_sequence` 补齐。业务侧只需：

1. 用 `im.history(id)` 初始化会话（开始跟踪）；
2. 在 `message` 事件里追加渲染（重复消息按 `msg.id` 幂等处理即可）；
3. 发送以 `sendMessage` 的返回值为准立即上屏，实时回包相同消息按 `id` 去重。

未被 `history()` 跟踪的会话不会自动拉消息（避免整段历史刷下来），
可通过 `event` 事件自行处理未读角标等场景。

## 小程序域名配置

在微信公众平台 → 开发管理 → 开发设置 → 服务器域名中配置：

- **request 合法域名**：`https://im.example.com`（REST + 上传）
- **socket 合法域名**：`wss://im.example.com`（实时网关）

本地调试可在开发者工具中勾选「不校验合法域名」并用 `http://127.0.0.1:1905` /
`ws://127.0.0.1:1910`。

## 在 Vue / React / 普通网页中使用

SDK 核心与平台无关；小程序端默认使用 `wechatAdapter`，浏览器端显式传入内置的
`browserAdapter`（fetch / WebSocket / FormData / localStorage）即可：

```ts
import { createIM, browserAdapter } from "airway-im-sdk-ts";

export const im = createIM({
  apiUrl: "https://im.example.com",
  wsUrl: "wss://im.example.com",
  credential: localStorage.getItem("im-credential") ?? "",
  adapter: browserAdapter(),
  getCredential: fetchFreshCredential, // 凭证过期时自动换新
});
```

行为与小程序端完全一致：`browserAdapter` 会在标签页切回前台（`visibilitychange`）
时自动重连并补同步；`im.uploadFile(file, dir)` 直接接受 `<input type="file">`
拿到的 `File` / `Blob` 对象（微信端仍传本地路径字符串）。

浏览器部署有两个额外注意点（小程序没有、浏览器才有）：

1. **REST 跨域（CORS）** — 后端目前没有 CORS 中间件，跨域请求会被浏览器拦截。
   推荐用 Nginx 把 API 反代成同源路径，或在后端加 CORS 中间件。
2. **网关 Origin 校验** — 浏览器的 WebSocket 握手带 `Origin` 头，需把前端域名
   加入网关的 `GATEWAY_ALLOWED_ORIGINS`（逗号分隔），否则握手被拒绝。

凭证仍须由你的业务后端签发后下发给前端，`IM_AUTH_SECRET` 不能出现在浏览器代码中。

其他平台（支付宝 / 抖音小程序、Node 等）按 `src/adapter.ts` 的 `IMAdapter`
接口提供自己的适配器即可，`test/node-adapter.ts` 是可参考的 Node 实现。

## 本地开发与验证

```bash
pnpm install
pnpm build          # 输出 dist/esm + dist/cjs（含 .d.ts）
pnpm typecheck
```

对真实后端栈（backend :1905 / gateway :1910 / delivery :1920，见仓库根 README）
做端到端验证：

```bash
IM_INTERNAL_URL=http://127.0.0.1:1906 pnpm verify
```

脚本覆盖：凭证铸造与注册、单聊/群聊收发、实时 fan-out 与顺序、幂等重放与 11002、
成员增删与事件、被踢成员权限、断线重连补同步、审核掩码 `message.updated`、
文件上传；随后以 `browserAdapter` 重跑核心链路（含 File/Blob 上传与 localStorage
持久化）。`IM_INTERNAL_URL`/`IM_INTERNAL_SECRET` 等可用环境变量覆盖。

## 协议参考

- API 契约：[`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- 网关协议：[`deps/im/docs/design/gateway.md`](../../../deps/im/docs/design/gateway.md)
- 凭证签发：[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)

---

English version: [README.md](README.md)
