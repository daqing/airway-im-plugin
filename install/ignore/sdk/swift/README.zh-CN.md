# airway-im-sdk-swift

[Airway IM](https://github.com/daqing/airway-im-plugin) 的 Swift SDK:把后端的
REST API、WebSocket 网关协议和服务端到服务端的凭证铸造接口封装成开箱即用的
Swift 6 接口,iOS / macOS 应用与服务端 Swift 不必自己实现凭证、网关首帧认证、
心跳、断线重连退避、基于 sequence 的补拉同步。

基于 Swift 6 并发(actors、`async`/`await`、`@Sendable`)构建,**零第三方依赖**
——只使用 Foundation 和 CryptoKit 系统框架。版本与其他 Airway IM SDK 保持一致
(当前 0.7.0;公共 API 与 `airway-im-sdk-ts` 一一对应)。

## 特性

- **REST 全覆盖** —— 用户资料、会话(直聊 get-or-create / 群聊 / 成员管理)、
  消息(历史、发送、幂等重试)、文件上传,全部强类型,自动解包
  `{code, data, message}` 信封。
- **会话句柄** —— `createDirect` / `getDirect` / `createGroup` /
  `openConversation` 返回每个会话的对象(`DirectConversation` /
  `GroupConversation`),自带会话级 `onMessage` 事件和 `send` / `history`;
  类型长在对象上,第一个消息监听器自动开始跟踪。
- **实时网关** —— 自动完成 `{"cmd":"auth"}` 首帧认证、应用层心跳(默认 25 s)、
  指数退避重连(0.5 s → 10 s)、按 `event_id` 去重。
- **同步引擎** —— 维护每个会话的 `sequence` 游标:`message.created` 事件到达时
  通过 `after_sequence` 补齐缺口,乱序、重复、离线期间漏掉的消息最终都会通过
  `message` 事件**按序、不重不漏**地送达。游标可持久化到 `UserDefaults`,冷启动
  只拉增量。
- **凭证自动续期** —— 凭证过期(HTTP 401/10001 或网关认证失败)时,SDK 自动调用
  `getCredential` 换取新凭证并恢复,业务代码无感。
- **服务端凭证铸造** —— Swift 后端(Vapor 等)通过 `InternalClient` 获取凭证
  (服务端到服务端,内网;绝不能链接进客户端代码),不必手写 HTTP 调用。
- **管理端操作** —— `AdminClient` 覆盖管理控制台 API(状态、用户、吊销、消息
  审核、违规处理),自动维护会话。

## 安装

Swift Package Manager。包位于本仓库 `install/ignore/sdk/swift/` 目录,发布版本
与其他 Airway IM SDK 同步打 tag(当前 0.7.0)。

- **Xcode**:*File → Add Package Dependencies… → Add Local…* 选择 `swift/`
  目录,再把 `AirwayIM` product 加到应用 target。
- **Package.swift**(开发期按路径引用,发布后改用发布仓库地址):

```swift
dependencies: [
    .package(name: "AirwayIM", path: "../airway-im-plugin/install/ignore/sdk/swift")
]
```

需要 Swift 6 工具链;iOS 15+ / macOS 12+ / tvOS 15+ / watchOS 8+。

## 快速上手

### 1. 由你自己的后端下发凭证

SDK 不包含登录逻辑。用户先在你的平台登录(密码、短信验证码等);登录成功后,
**你平台自己的服务端后端**从自己的用户表读取 `(uuid, name)`,通过**服务端到
服务端**调用内部铸造接口(受 `IM_INTERNAL_SECRET` 保护)获取凭证,随你自己的
登录响应一起返回给客户端。Swift 后端用 `InternalClient`(仅服务端,绝不能链接
进客户端代码):

```swift
import AirwayIM

let internal = InternalClient(
    internalURL: "http://127.0.0.1:1906",   // 内部监听地址,仅限内网
    internalSecret: ProcessInfo.processInfo.environment["IM_INTERNAL_SECRET"]!)

let minted = try await internal.mintCredential(
    uuid: user.uuid,             // 来自你自己的用户表
    name: user.username,
    nickname: user.displayName,  // 可选;只在传了才写入,不会覆盖已有资料
    ttlSeconds: 86_400)          // 可选;默认 24 h,0 = 永不过期

minted.credential  // "im1.…" —— 随登录响应下发给客户端
minted.expiresAt   // RFC 3339 UTC,到期前需重新铸造;ttlSeconds: 0 时为 nil
```

其他语言的后端用普通 HTTP 调同一接口。签名密钥 `IM_AUTH_SECRET` 只存在于
Airway 项目的 IM 服务端;你的后端只需要 `IM_INTERNAL_SECRET`。客户端不持有任何
密钥,只持有最终凭证(REST:`Authorization: Bearer`;WebSocket:首条 `auth`
命令)。

**前提:你的平台必须有自己的服务端后端。** 纯客户端 App 没有服务器无法安全接入
——客户端没有安全获取凭证的途径。完整信任模型见
[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)。

### 2. 在 iOS / macOS 应用里收发消息

```swift
import AirwayIM

let im = AirwayIM(
    apiURL: "https://im.example.com",   // IM 后端(:1905),生产环境必须 https
    wsURL: "wss://im.example.com",      // WebSocket 网关(:1910);SDK 连接 <wsURL>/ws
    credential: storedCredential,
    getCredential: {                    // 凭证失效时自动调用
        await fetchNewCredentialFromYourBackend()
    })

// 连接控制
im.onStatus { status in print("connection:", status) }
await im.connect()

// 直聊:类型长在对象上
let direct = try await im.createDirect(otherUserUUID)   // get-or-create
direct.onMessage { message in
    // 按序、去重、补齐缺口(包括离线期间漏掉的);第一个监听器自动开始跟踪
    print(message.sender.nickname ?? message.sender.username, message.content)
}
let backlog = try await direct.history()   // 目前为止的历史,sequence 升序

// 发送(SDK 自动生成 Idempotency-Key,网络失败用同一个 key 重试,消息绝不重复)
try await direct.send("你好", contentType: .plain)

// 群聊:同样的模型
let group = try await im.createGroup(title: "Team", memberUUIDs: [otherUserUUID])
group.onMessage { message in print(message.content) }
try await group.send("hello")
try await group.addMembers([anotherUserUUID])
```

### 3. 应用生命周期建议

iOS 从 13 起对后台 socket 更宽容,但长时间后台仍会断开。应用回到前台时再调一次
`connect()`(幂等);SDK 会自动重连并补拉同步:

```swift
NotificationCenter.default.addObserver(
    forName: UIApplication.willEnterForegroundNotification,
    object: nil, queue: .main) { _ in
        Task { await im.connect() }
    }
```

## API 参考

### `AirwayIM` 参数

| 参数 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `apiURL` | ✓ | — | IM 后端地址(:1905);生产环境 https |
| `wsURL` | | — | 网关基础地址(`:1910`)**不带路径**,SDK 自己追加 `/ws`;不传则只用 REST,无实时 |
| `credential` | ✓ | — | 你的后端下发的用户凭证 |
| `getCredential` | | — | 凭证被拒(HTTP 401/10001 或网关认证失败)时获取新凭证;每个请求最多续期一次、重试一次 |
| `timeout` | | `15` | REST 请求超时(秒) |
| `pingInterval` | | `25` | 应用层心跳间隔(秒),`0` 关闭 |
| `persistSequences` | | `true` | 持久化每个会话的 sequence 游标 |
| `sequenceStore` | | `UserDefaults` | 游标存储位置(`InMemorySequenceStore()` 仅内存) |
| `httpTransport` / `socketFactory` | | URLSession | 自定义传输层接缝,用于测试与代理 |
| `autoConnect` | | `false` | 创建后立即连接网关 |

### REST 方法(`im.*`)

| 方法 | 端点 |
| --- | --- |
| `im.me()` | `GET /api/v1/me` |
| `im.listGroups()` | `GET /api/v1/conversations?type=group` |
| `im.createDirect(otherUserUUID)` | 直聊 get-or-create;返回 `DirectConversation` 句柄 |
| `im.getDirect(otherUserUUID)` | 已存在的直聊句柄(不存在返回 nil) |
| `im.createGroup(title:memberUUIDs:)` | `POST /api/v1/group`;返回 `GroupConversation` 句柄 |
| `im.openConversation(id)` | 按任意会话 id 打开会话句柄(类型取自注册表,否则一次 REST 查询) |
| `im.addMembers(conversationId, memberUUIDs:)` | `POST .../members` |
| `im.removeMembers(conversationId, userUUIDs:)` | `DELETE .../members/:user_uuid` |
| `im.history(conversationId, fromSequence:limit:)` | 拉取历史并开始跟踪同步 |
| `im.listMessages(conversationId, afterSequence:limit:)` | `GET .../messages`(原始分页) |
| `im.sendGroupMessage(conversationId, content, …)` | `POST /api/v1/messages` |
| `im.sendDirectMessage(otherUserUUID, content, …)` | get-or-create 直聊会话后 `POST /api/v1/messages` |
| `im.uploadFile(file, dir:)` | `POST /api/v1/storage`(multipart;`FileInput` = `.fileURL(URL)` 或 `.data(Data, filename:)`) |
| `im.storageURL(key)` | 文件下载 URL |

发送参数:`contentType`(`.markdown` 默认 / `.plain`)、`idempotencyKey`
(默认自动生成)、`retries`(网络失败重试次数,默认 1)。

### 会话句柄(`DirectConversation` / `GroupConversation`)

句柄是每个会话一个的对象——类型长在对象上,同一会话总是返回同一句柄。会话句柄
上发出的事件也会出现在门面的全局流上(见下),反之亦然。

| 成员 | 说明 |
| --- | --- |
| `id` / `kind` | 会话 id;`.direct` 或 `.group` |
| `onMessage(…)` / `onMessageUpdated(…)` | 按序消息事件;群聊还会触发 `onMembersAdded` / `onMembersRemoved`。第一个 `onMessage` 监听器自动开始跟踪(从上次持久化游标拉历史,然后实时)。每次注册返回 `Subscription`,调 `cancel()` 退订 |
| `history(fromSequence:limit:)` | 等待拉完积压;每条消息通过 `message` 事件发出 |
| `send(content, …)` | 向该会话发送(幂等/重试语义同 `sendGroupMessage`) |
| `listMessages(afterSequence:limit:)` | 原始有序分页,不改状态 |
| `lastSequence()` / `forget()` | 同步游标;丢弃该会话的全部状态 |
| `details()` | 会话类型 + 带角色的成员,实时来自 API |
| 群聊专属:`title` | 创建时的标题(按 id 打开时为 nil) |
| 群聊专属:`addMembers(_:)` / `removeMembers(_:)` | 成员管理;返回带角色的成员 |

### 全局流(`im.on…`)

会话句柄是单窗口 API;门面同时提供全局流,便于未读角标、统一收件箱:

| 注册方法 | 载荷 | 说明 |
| --- | --- | --- |
| `onMessage` | `(ChatMessage)` | 新消息,按序、去重、补缺口 |
| `onMessageUpdated` | `(ChatMessage)` | 消息被审核屏蔽;content 已是 `***`;整条替换渲染 |
| `onMembersAdded` | `MembersAddedInfo` | 群成员新增(包括被加入的用户自己) |
| `onMembersRemoved` | `MembersRemovedInfo` | 群成员被移出(被踢用户也会收到) |
| `onStatus` | `ConnectionStatus` | `connecting / authenticating / online / reconnecting / offline / closed` |
| `onError` | `IMError` | 网关认证失败、凭证续期失败等 |
| `onEvent` | 原始 `GatewayEvent` | 每一帧网关事件;尚未跟踪的会话的消息也在这里到达 |

连接控制:`im.connect()`(幂等)/ `im.disconnect()` / `await im.isOnline` /
`im.connectionStatus` / `await im.setCredential(_)` /
`await im.lastSequence(conversationId)` /
`await im.forgetConversation(conversationId)`(如被踢出群后丢弃同步游标)。

### 消息模型(`ChatMessage`)

`send`、`sendDirectMessage`、`listMessages`、`history` 和实时 `message`
事件都携带同一种 `ChatMessage`:

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string,26 位 ULID | 服务端生成的全局唯一消息标识;去重的稳定键。 |
| `conversationId` | string,26 位 ULID | 消息所属会话;按它把消息路由到聊天窗口。 |
| `sender.uuid` | string | 发送者的稳定身份 uuid(API 与事件全程只用 uuid 标识身份)。 |
| `sender.username` | string | 宿主分配的账号名,账号生命周期内稳定。 |
| `sender.nickname` | string? | 展示名优先;为 nil 时回退到 `username`。 |
| `sender.avatarUrl` | string? | 头像地址;为 nil 时渲染占位图。 |
| `content` | string | 消息体,1–32768 UTF-8 字节,服务端把 CRLF 规范为 LF。被审核的消息读回字面 `***`。 |
| `contentType` | string | `text/markdown`(默认)或 `text/plain` —— 决定 `content` 如何渲染。 |
| `createdAt` | string,RFC 3339 UTC | 服务端提交时间戳;展示时转换为用户本地时区。 |
| `sequence` | integer ≥ 1 | 提交时分配的会话内位置。`(conversationId, sequence)` 是排序的总顺序。 |

排序用 `sequence`,不要用 `createdAt`。同一幂等键的重试返回原消息(相同 `id` 与
`sequence`);实时事件的去重与补洞 SDK 已经处理。`sender` 反映作者当前资料,
不是发送时快照。

### `InternalClient`(内部铸造接口,默认 `127.0.0.1:1906`)

| 方法 | 说明 |
| --- | --- |
| `mintCredential(uuid:name:nickname:avatarUrl:ttlSeconds:)` | 服务端到服务端铸造凭证;`ttlSeconds` 默认 86400,上限 2592000,`0` 表示永不过期;返回 `MintedCredential`(`credential`、`expiresAt`) |

该类型只属于你的后端——客户端 App 绝不能调用:谁能铸造凭证谁就能冒充任意用户。
此处错误码 `10005` 表示 `X-IM-Internal-Secret` 缺失或错误(不是公共 API 的权限
不足);`10006` 表示部署未配置 `IM_AUTH_SECRET`,无法签名。

### `AdminClient`(管理控制台,`/admin/api`)

首次调用自动登录(12 小时会话);会话中途 401 触发一次重登并重试。响应为
`JSONValue`(管理端结构开放;见
[`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md))。

| 方法 | 说明 |
| --- | --- |
| `login` / `logout` | 显式登录 / 登出 |
| `status` | 聚合状态:用户数、outbox、网关/投递指标 |
| `users` | 已注册用户(含 `token_version`) |
| `revokeUser(uuid)` | 吊销用户全部凭证并踢掉其连接 |
| `groupConversations` | 所有群聊(含成员/消息计数) |
| `conversationMessages(id)` | 消息审核(升序) |
| `markIllegal(messageId)` | 标记违规(幂等):客户端读回 `***` |

### `Credentials`(签名与校验辅助)

`sign` 本地签名只面向可信持有 `IM_AUTH_SECRET` 的 Airway 项目自有侧;第三方
平台后端绝不持有该密钥,必须使用 `InternalClient`。(仅 Apple 平台——使用
CryptoKit 系统框架。)

| 函数 | 说明 |
| --- | --- |
| `sign(secret:uuid:name:nickname:avatarUrl:tokenVersion:ttl:now:)` | 签发 `im1.<payload>.<sig>` HMAC-SHA256 凭证;可选字段只在传了才写入,绝不覆盖既有资料 |
| `decode(credential)` | 返回 `Claims`(畸形输入抛错) |
| `verifySignature(credential, secret:)` | 常数时间校验,畸形输入返回 `false` |
| `isExpired(credential or claims, now:)` | `exp` 是否已过;无 `exp` 的凭证永不过期 |

### 错误处理

所有 REST 错误抛出 `IMError`(`error.code` 为信封业务码,`error.status` 为 HTTP
状态码,传输失败时 `status == 0`)。常用判断:

```swift
do {
    try await im.sendGroupMessage(id, "hi")
} catch let error as IMError {
    if error.isAuthError { /* 10001:凭证失效——等待自动续期或重新登录 */ }
    else if error.code == ErrorCode.permissionDenied { /* 10005:不是成员 */ }
    else if error.code == ErrorCode.conversationNotFound { /* 11001 */ }
}
```

全部错误码:`10000` 内部错误,`10001` 凭证无效,`10003` 请求非法,`10005` 权限
不足,`11001` 会话不存在,`11002` 幂等键与不同请求体复用。

## 消息可靠性模型

后端保证每个会话的 `sequence` 单调递增,投递是**至少一次**。SDK 同步引擎负责
按 `event_id` / `message_id` 去重、按 `sequence` 排序、通过 `after_sequence`
补洞。业务代码只需要:

1. 打开会话——句柄第一个 `onMessage`(或 `history()`)开始跟踪;
2. 在 `message` 事件里追加渲染(按 `message.id` 做幂等,重复消息不重复渲染);
3. 用 `send()` 的返回值乐观渲染,实时回声按 `id` 去重。

从未跟踪的会话不会自动拉取消息(避免全量历史冲刷);未读角标等场景通过
`onEvent` 处理。

## 并发模型

`AirwayIM`、`RESTClient`、`AdminClient` 与内部引擎类型都是 actor:每个方法都是
`async`,可在任意任务中调用,实例可放心全局共享。事件处理器是 `@Sendable` 闭包,
在任意 executor 上回调——触碰 UI 前请自行切回主 actor。`Subscription.cancel()`
用于退订;监听器需要生效多久,就保住返回的 Subscription 多久。

## 本地开发与验证

```bash
swift build
swift test        # 45 个测试:签名向量、信封解析、幂等重试、凭证续期、
                  # 同步引擎、网关重连/认证流程——全部打在纯标准库的桩服务器上
```

测试中的跨语言签名向量与 Node.js 参考实现
(`deps/im/docs/design/identity.md` §4.2)逐字节对齐,与 Ruby、PHP 测试套用的是
同一向量。如需对真实栈(后端 :1905 / 网关 :1910 / 内部 :1906,见仓库根 README)
做端到端验证:启动整栈后按"快速上手"逐步执行。

## 协议参考

- API 契约:[`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- 网关协议:[`deps/im/docs/design/gateway.md`](../../../deps/im/docs/design/gateway.md)
- 凭证签发:[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)
- 管理控制台:[`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)

---

English version: [README.md](README.md)
