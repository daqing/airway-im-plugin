# airway-im-sdk-ts

[Airway IM](https://github.com/daqing/airway-im-plugin) 的 JS/TS SDK，覆盖
微信小程序与浏览器（Vue / React / 普通网页）：把后端的 REST API、WebSocket
网关协议与服务端对服务端的凭证签发接口封装成一套开箱即用的 TypeScript 接口，
客户端无需自己实现凭证、网关首帧认证、心跳、断线重连和序列补同步等协议代码。

零运行时依赖，输出 CommonJS + ESM 双格式（`dist/cjs` / `dist/esm`），同时兼容
微信开发者工具「构建 npm」、Taro / uni-app 以及 Vue / React 等浏览器框架。

## 功能

- **REST 全量封装** — 个人资料、会话（单聊 get-or-create / 群聊 / 成员管理）、
  消息（历史、发送、幂等重试）、文件上传，全部强类型并自动解 `{code,data,message}` 信封。
- **会话对象** — `createDirect` / `getDirect` /
  `createGroup` / `openConversation` 返回每个会话一个的对象
  （`DirectConversation` / `GroupConversation`），事件按会话作用域收发、自带
  `send()` / `history()`；类型就在对象上，首个 message 监听器自动开始跟踪。
- **实时网关** — 自动完成 `{"cmd":"auth"}` 首帧认证、应用层心跳（默认 25s）、
  指数退避断线重连（0.5s→10s）、按 `event_id` 去重。
- **同步引擎** — 维护每个会话的 `sequence` 游标：收到 `message.created` 事件自动
  按 `after_sequence` 补洞补齐，乱序 / 重复 / 掉线期间的消息最终都会**有序、不重、
  不漏**地通过 `message` 事件送达；游标可持久化到本地存储，冷启动只拉增量。
- **凭证续期** — 凭证过期（HTTP 401/10001 或网关认证失败）时自动回调
  `getCredential` 换新凭证并恢复，业务代码无感。
- **服务端凭证签发** — Node 后端通过 SDK 的 `InternalClient` 获取凭证
  （服务端对服务端、仅限内网；绝不要打包进客户端代码），无需手写 HTTP 调用。
- **小程序友好** — 内置的 `wechatAdapter` 基于 `wx.request` / `wx.connectSocket` /
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

Node 后端里这次调用用 SDK 的 `InternalClient` 一步完成（仅限服务端，
绝不要打包进客户端代码）：

```ts
import { InternalClient } from "airway-im-sdk-ts";

const internal = new InternalClient({
  internalUrl: "http://127.0.0.1:1906",            // 内部监听地址，仅限内网
  internalSecret: process.env.IM_INTERNAL_SECRET!, // 该部署的 IM_INTERNAL_SECRET
});

const minted = await internal.mintCredential({
  uuid: user.uuid,             // 来自你自己的用户表
  name: user.username,
  nickname: user.displayName,  // 可选；仅在传入时写入
  ttlSeconds: 86_400,          // 可选；默认 24 小时，0 表示永不过期
});

minted.credential;  // "im1.…" — 随登录响应下发给客户端
minted.expiresAt;   // RFC 3339 UTC，到期重签时间；ttlSeconds 为 0 时为 null
```

其他语言的后端直接走普通 HTTP 调用同一端点；完整契约见下文 API 一览中的
「凭证签发接口」小节。

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
import { createClient, wechatAdapter } from "airway-im-sdk-ts";

const im = createClient({
  apiUrl: "https://im.example.com",   // IM 后端（:1905），必须是 https
  wsUrl: "wss://im.example.com",      // WebSocket 网关（:1910）；SDK 实际连接 <wsUrl>/ws
  credential: wx.getStorageSync("im-credential"),
  adapter: wechatAdapter(),           // 必填：浏览器/Node 传 browserAdapter()
  getCredential: () =>                // 凭证失效时自动调用
    fetchNewCredentialFromYourBackend(),
});

im.on("status", (s) => console.log("connection:", s));
im.connect();

// 单聊：类型就在对象上
const direct = await im.createDirect(otherUserUUID);   // get-or-create
direct.on("message", (msg) => {
  // 有序、去重、自动补洞（含掉线期间错过的）；首个监听器自动开始跟踪
  console.log(msg.sender.nickname, msg.content);
});
const history = await direct.history();   // 目前为止的离线消息，按 sequence 升序

// 发送（SDK 自动生成 Idempotency-Key，网络失败自动用同一 key 重试，不会重发）
await direct.send("你好", { contentType: "text/plain" });

// 群聊：同样的模型
const group = await im.createGroup("Team", [otherUserUUID]);
group.on("message", (msg) => console.log(msg.content));
await group.send("hello");
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

### `createClient(options)`

| 选项 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `apiUrl` | ✓ | — | IM 后端地址（:1905），生产必须 https |
| `wsUrl` | | — | 网关 base URL（`:1910`）——只传主机部分，**不要**自带路径；SDK 会自动追加 `/ws`（`wsUrl: "ws://localhost:1910"` → 默认网关端点 `ws://localhost:1910/ws`；反代终结 TLS 后用 `wss://…`）。不传则只用 REST，不连实时 |
| `credential` | ✓ | — | 业务后端签发的用户凭证 |
| `getCredential` | | — | `() => Promise<string>`，凭证失效时换新 |
| `adapter` | ✓ | — | 平台适配器：小程序传 `wechatAdapter()`，浏览器/Node 传 `browserAdapter()`；必填、不设默认值，缺失时 `createClient` 直接抛异常 |
| `timeoutMs` | | `15000` | REST 请求超时 |
| `pingIntervalMs` | | `25000` | 应用层心跳间隔，`0` 关闭 |
| `persistSequences` | | `true` | 持久化各会话 sequence 游标 |
| `autoConnect` | | `false` | 创建后立即连网关 |

### REST 方法（`im.*`）

| 方法 | 对应接口 |
| --- | --- |
| `im.me()` | `GET /api/v1/me` |
| `im.listGroups()` | `GET /api/v1/conversations?type=group` |
| `im.createDirect(otherUserUUID)` | 单聊 get-or-create；返回 `DirectConversation` 会话对象 |
| `im.getDirect(otherUserUUID)` | 已有单聊的会话对象（没有则返回 null） |
| `im.createGroup(title, memberUUIDs)` | `POST /api/v1/group`；返回 `GroupConversation` 会话对象 |
| `im.openConversation(id)` | 按会话 id 打开会话对象（注册表已知直接返回，否则拉一次 REST） |
| `im.addMembers(conversationId, memberUUIDs)` | `POST .../members` |
| `im.removeMembers(conversationId, userUUIDs: string[])` | `DELETE .../members/:user_uuid` |
| `im.history(conversationId, {fromSequence?, limit?})` | 拉历史 + 开始跟踪同步 |
| `im.listMessages(conversationId, {afterSequence?, limit?})` | `GET .../messages`（原始分页） |
| `im.sendGroupMessage(conversationId, content, opts?)` | `POST /api/v1/messages` |
| `im.sendDirectMessage(otherUserUUID, content, opts?)` | Get-or-create 单聊会话后走 `POST /api/v1/messages` |
| `im.uploadFile(filePath \| File, dir?)` | `POST /api/v1/storage` |
| `im.storageUrl(key)` | 文件下载地址（配 `<image>` / `wx.downloadFile`） |

`sendGroupMessage` 的 `opts`：`contentType`（`text/markdown` 默认 / `text/plain`）、
`idempotencyKey`（默认自动生成）、`retries`（网络失败重试次数，默认 1）。

### 会话对象（`DirectConversation` / `GroupConversation`）

会话对象即"每个会话一个对象"——类型就在对象上，同一会话永远返回同一个
对象。会话对象上的事件同样会出现在门面的全局流里（见下），反之亦然。

| 成员 | 说明 |
| --- | --- |
| `id` / `kind` | 会话 id；`"direct"` 或 `"group"` |
| `on(event, listener)` | `message` `(msg)`、`message.updated` `(msg)`；群对象还有 `members.added` / `members.removed`。首个 `message` 监听器自动开始跟踪（按游标补历史，之后走实时） |
| `history({fromSequence?, limit?})` | 等待积压拉完；以 `"history"` 来源触发 `message` |
| `send(content, opts?)` | 向该会话发送（幂等/重试语义同 `sendGroupMessage`） |
| `lastSequence()` / `forget()` | 同步游标；丢弃该会话的全部同步状态 |
| `details()` | 会话类型 + 成员与角色，实时来自 API |
| 群对象独有：`title` | 创建时的标题 |
| 群对象独有：`addMembers(uuids)` / `removeMembers(uuid)` | 成员管理；成员与角色列表 |

```ts
const group = await im.createGroup("Team", [aliceUuid, bobUuid]);
group.on("message", (msg) => renderGroupMessage(msg));
await group.send("hello");
```

### Message 对象（`ChatMessage`）

`sendGroupMessage`、`sendDirectMessage`、`listMessages`、`history` 以及实时
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

### 全局流（`im.on(name, handler)`）

会话对象是按窗口收发的首选 API；门面同时暴露一条全局流，适合未读角标、
统一收件箱这类场景：

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

全局 `message` 事件只带 `conversation_id`——线路上没有会话类型。需要类型
或按会话订阅时，用 `im.openConversation(msg.conversation_id)` 打开会话对象。

### 错误处理

所有 REST 错误抛出 `IMError`（`err.code` 信封业务码、`err.status` HTTP 状态码，
传输失败时 `status === 0`）。常用判断：

```ts
import { IMError, ErrorCode } from "airway-im-sdk-ts";

try {
  await im.sendGroupMessage(id, "hi");
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

### 凭证签发接口（服务端之间）

TS/Node 后端请使用 SDK 的 `InternalClient`（见快速开始第 1 步）；该端点本身
就是普通 HTTP，任意语言的后端都可以直接调用。

`POST /internal/v1/credentials`，作用于 IM 服务的内部监听地址（默认
`127.0.0.1:1906`，仅限内网），以 `X-IM-Internal-Secret` 头认证（值为该部署
的 `IM_INTERNAL_SECRET`）。该接口只允许你的后端调用——浏览器页面、小程序
绝不能直接调用：能签发凭证的一方，就能冒充任意用户。

首次签发时，用户会以你提供的 `uuid` 在 IM 侧注册（之后客户端调
`createDirect` / `sendDirectMessage` 用的就是这个 uuid）；签出的凭证携带
该用户当前的 `token_version`，因此通过管理端 API 吊销该用户后，此前签发的
所有凭证立即失效。

请求体（JSON）：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `uuid` | ✓ | 你自己用户表里的稳定用户 ID，1–64 字符 |
| `name` | ✓ | 账号名，1–64 字符 |
| `nickname` | | 显示昵称；仅在传入时写入，不传不会覆盖之前存过的资料 |
| `avatar_url` | | 头像 URL，最长 2048 字符；同样仅在传入时写入 |
| `ttl_seconds` | | 凭证有效期（秒）；默认 `86400`（24 小时），上限 `2592000`（30 天），`0` 表示永不过期 |

成功返回 `{code: 0, data: {credential, expires_at?}}`：`credential` 即签好
的 `im1.<payload>.<sig>` 凭证，客户端以 `Authorization: Bearer`（REST）或
首条网关 `auth` 命令出示；`expires_at`（RFC 3339 UTC）在 `ttl_seconds` 为
`0` 时省略。

错误码：`10003` JSON 非法、`uuid`/`name` 缺失或超长、`ttl_seconds` 越界；
`10005` 在这里表示 `X-IM-Internal-Secret` 缺失或不匹配（不是公开 API 的
无权限）；`10006` Airway 部署未配置 `IM_AUTH_SECRET`、无法签发；
`10000` 内部错误。

## 消息可靠性模型

后端保证每个会话内 `sequence` 单调递增，投递是 **at-least-once**。SDK 的同步引擎
负责：按 `event_id` / `message_id` 去重、按 `sequence` 排序、发现缺口时用
`after_sequence` 补齐。业务侧只需：

1. 打开会话——会话对象的首个 `on("message")`（或 `history()`）即开始跟踪；
2. 在 `message` 事件里追加渲染（重复消息按 `msg.id` 幂等处理即可）；
3. 发送以 `send()` 的返回值为准立即上屏，实时回包相同消息按 `id` 去重。

未跟踪的会话不会自动拉消息（避免整段历史刷下来），可通过全局 `event`
事件自行处理未读角标等场景。

## 小程序域名配置

在微信公众平台 → 开发管理 → 开发设置 → 服务器域名中配置：

- **request 合法域名**：`https://im.example.com`（REST + 上传）
- **socket 合法域名**：`wss://im.example.com`（实时网关）

本地调试可在开发者工具中勾选「不校验合法域名」并用 `http://127.0.0.1:1905` /
`ws://127.0.0.1:1910`（SDK 实际连接的网关端点是 `ws://127.0.0.1:1910/ws`；
网关本身只提供明文 WS，`wss` 需由反向代理终结 TLS）。

## 在 Vue / React / 普通网页中使用

SDK 核心与平台无关，必须显式传适配器、没有默认值：小程序传 `wechatAdapter()`，
浏览器/Node 传内置的 `browserAdapter()`（fetch / WebSocket / FormData /
localStorage）：

```ts
import { createClient, browserAdapter } from "airway-im-sdk-ts";

export const im = createClient({
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
