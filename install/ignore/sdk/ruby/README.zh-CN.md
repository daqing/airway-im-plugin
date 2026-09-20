# airway-im-sdk-ruby

[Airway IM](https://github.com/daqing/airway-im-plugin) 的 Ruby SDK：把后端的
REST API 与凭证铸造接口封装成一套开箱即用的 Ruby 接口，Ruby 项目
（Rails / Sinatra / 任意 Ruby 服务）无需自己实现铸造调用、信封解析、幂等重试
和凭证自动续期等协议代码。

零运行时依赖 —— 只使用 Ruby 标准库（`Net::HTTP` / `JSON` / `OpenSSL` /
`SecureRandom`）。

**定位**：这是一个**服务端 SDK**。它覆盖 Ruby 后端集成 IM 的全部场景——为你
的用户签发/铸造凭证、以任意用户身份调用 IM API、管理后台操作。浏览器/小程序
里的实时 WebSocket 收发请配合前端使用 JS/TS SDK
（[`airway-im-sdk-ts`](../ts/)，含同步引擎）；Ruby 端如需监听新消息（机器人、
通知），用序列同步接口轮询即可（见下文 `each_message`）。

## 安装

```bash
gem install airway-im-sdk-ruby
```

或在 Gemfile 中：

```ruby
gem "airway-im-sdk-ruby"
```

```ruby
require "airway-im-sdk-ruby"
```

## 快速开始

### 1. 为你的用户获取凭证

SDK 不含登录逻辑。用户先在你的平台完成自己的登录（账号密码、验证码等）；
登录成功后，**你的服务器后端**从自己的用户表取出 `(uuid, name)`，以服务端对
服务端方式调用 IM 服务的**内部铸造接口**获得凭证，并随你自己的登录响应下发
给客户端。你的后端只需持有 `IM_INTERNAL_SECRET`，签名密钥 `IM_AUTH_SECRET`
永远留在 IM 服务端；铸造出的凭证带 `token_version`，可通过管理后台逐用户
吊销：

```ruby
internal = AirwayIM::InternalClient.new(
  internal_url: "http://127.0.0.1:1906",   # 内部监听地址，仅限内网
  internal_secret: ENV["IM_INTERNAL_SECRET"],
)

minted = internal.mint_credential(uuid: "user-42", name: "alice",
                                  nickname: "Alice", ttl_seconds: 86_400)
credential = minted.credential          # "im1.…"
minted.expires_at                       # ISO8601 字符串；ttl_seconds: 0 时为 nil
```

第三方平台后端**只能**通过内部铸造接口获取凭证：本地签名（自行持有
`IM_AUTH_SECRET` 计算 HMAC）仅面向 Airway 项目方自己的后端，平台侧一律不
持有签名密钥。

两个必须分清的点（完整信任模型见
[identity.md](../../../deps/im/docs/design/identity.md)）：

- 凭证永远由**你的后端**获取并下发，客户端从不向 IM 服务器索取凭证；
- 铸造接口默认只监听 `127.0.0.1:1906`，跨机部署时经内网调用，不向公网暴露。
  **没有服务器后端的纯前端无法安全接入。**

### 2. 调用 IM API

所有方法返回信封 `data`（Hash 或 Array），失败抛出 `AirwayIM::Error`。消息类
响应返回 `AirwayIM::Message`——一个 Hash 子类，`message["id"]`、`message.id`、
`message.sender.uuid` 均可使用：

```ruby
im = AirwayIM::Client.new(
  api_url: "https://im.example.com",
  credential: credential,
  get_credential: -> { mint_fresh_credential },  # 可选：凭证失效时自动换新并重试一次
)

direct = im.create_direct("user-2")       # DirectConversation 会话对象（get-or-create）
im.get_direct("user-2")      # 与 user-2 的已有会话对象，没有则返回 nil（只读）
group = im.create_group(member_uuids: ["user-2", "user-3"], title: "Backend Team")
im.list_groups                            # 我加入的群列表
group.details                             # 会话类型 + 成员及角色
im.open_conversation(group.id)            # 按任意会话 id 打开会话对象

# 发送消息：自动生成 Idempotency-Key，网络失败自动用同一 key 重试，不会重发
direct.send_message("你好")
message = im.send_direct_message("user-2", "你好") # 一步到位：get-or-create 单聊会话后直接发送
message.sequence                          # Message 是带读取方法的 Hash，message["sequence"] 也可以

group.add_members(["user-4"])             # 群专属操作直接在会话对象上
group.remove_members("user-4")
group.details                             # 成员及角色，实时来自 API

# 历史 / 序列同步（掉线后从上次游标补齐，升序返回）
im.list_messages(direct.id, after_sequence: 42, limit: 100)
im.each_message(direct.id).each { |msg| … }   # 自动翻页遍历全部历史

im.upload_file("/path/to/avatar.png", dir: "avatars")   # 返回 {key, url, size}
im.storage_url("avatars/202609/xxx.png")                # 下载地址
```

服务账号（机器人 / 系统通知）同样走这条路径：给机器人分配一个固定
`(uuid, name)`，铸造一个长效凭证，即可用它建会话、发消息。

## API 一览

### `AirwayIM::Client`（公共 API，默认 `:1905`）

方法名与 TS SDK 一一对应（这边 snake_case，那边 camelCase）：
`create_direct` ↔ `createDirect`、`get_direct` ↔
`getDirectConversation`、`create_group` ↔
`createGroup`、`list_messages` ↔ `listMessages` 等。会话对象同样齐备——
`DirectConversation` / `GroupConversation` 提供 `send_message` /
`list_messages` / `each_message` / `details`（群会话另有 `add_members` /
`remove_members`），以及 `open_conversation(id)`；缺的只是实时
层——TS 会话对象上的 `on("message")` 订阅、WebSocket 网关和同步引擎，
v1 的 Ruby SDK 以轮询 `each_message` 代替。

| 方法 | 对应接口 |
| --- | --- |
| `me` | `GET /api/v1/me` |
| `list_groups` | `GET /api/v1/conversations?type=group` |
| `create_direct(other_uuid)` | 同上（direct get-or-create） |
| `get_direct(other_uuid)` | `GET /api/v1/conversations/direct/:user_uuid`（没有则返回 nil） |
| `create_group(member_uuids:, title: nil)` | `POST /api/v1/group` |
| `open_conversation(conversation_id)` | `GET /api/v1/conversations/:uuid`，以会话对象返回（从详情解析类型/标题） |
| `add_members(conversation_id, member_uuids)` | `POST .../members`（owner/admin，幂等） |
| `remove_members(conversation_id, user_uuids)` | `DELETE .../members/:user_uuid`（owner/admin） |
| `list_messages(conversation_id, after_sequence: nil, limit: nil)` | `GET .../messages`（原始分页，limit 1–200） |
| `each_message(conversation_id, after_sequence: 0, page_size: 100)` | 自动翻页的 Enumerator，升序遍历历史 |
| `send_group_message(conversation_id, content, content_type:, idempotency_key:, retries:)` | `POST /api/v1/messages` |
| `send_direct_message(other_uuid, content, …)` | Get-or-create 单聊会话后走 `POST /api/v1/messages` |
| `upload_file(path 或 IO, filename:, dir:)` | `POST /api/v1/storage`（multipart） |
| `storage_url(key)` | 文件下载地址 |

构造选项：`api_url`（必填）、`credential`（必填）、`get_credential`（可选
callback，收到 `10001`/401 时自动换新凭证并重试一次；`credential` 字段随之
更新）、`timeout`（秒，默认 15）。`content_type` 支持 `text/markdown`（默认）
与 `text/plain`；内容上限 32768 字节，服务端会把 CRLF 归一为 LF。

### 会话对象

`create_direct` 和 `get_direct` 返回 `AirwayIM::DirectConversation`；
`create_group` 和 `open_conversation` 通过详情查询解析为
`AirwayIM::GroupConversation`（或 `DirectConversation`）。每个会话对象都携带
`id` 与 `kind`，并把会话 API 收拢到自身：

| 会话对象方法 | 委托到 |
| --- | --- |
| `send_message(content, content_type:, idempotency_key:, retries:)` | `POST /api/v1/messages` |
| `list_messages(after_sequence:, limit:)` | `list_messages(id, …)` |
| `each_message(after_sequence: 0, page_size: 100)` | `each_message(id, …)` |
| `add_members(member_uuids)` —— 仅群 | `add_members(id, member_uuids)` |
| `remove_members(user_uuids)` —— 仅群 | `remove_members(id, user_uuids)` |
| `details` | `GET /api/v1/conversations/:uuid` |

`group.title` 携带创建时的标题（未传或按 id 打开时为 `nil`）；`details`
返回实时值。

### Message 对象

`send_message`、`send_direct_message`、`list_messages`、`each_message` 返回
`AirwayIM::Message`——一个 Hash 子类，`message["id"]`、`message.id`、
`message.sender.uuid` 均可使用：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string，26 位 ULID | 服务端生成的全局唯一消息 ID；去重的稳定键。 |
| `conversation_id` | string，26 位 ULID | 消息所属会话；据此路由到对应的聊天窗口。 |
| `sender.uuid` | string | 作者的稳定身份 uuid（API 与事件中的身份一律用 uuid）。 |
| `sender.username` | string | 宿主应用分配的账号名，账号生命周期内稳定。 |
| `sender.nickname` | string \| nil | 显示昵称；为 nil 时回退用 `username` 渲染。 |
| `sender.avatar_url` | string \| nil | 头像 URL；为 nil 时渲染占位图。 |
| `content` | string | 消息正文，1–32768 个 UTF-8 字节，服务端已把 CRLF 归一为 LF。被审核屏蔽的消息读回字面量 `***`。 |
| `content_type` | string | `text/markdown`（默认）或 `text/plain`——决定 `content` 的渲染方式。 |
| `created_at` | string，RFC 3339 UTC | 服务端提交时间戳；展示时换算为用户本地时区。 |
| `sequence` | integer ≥ 1 | 消息在会话内的位置，提交时分配。`(conversation_id, sequence)` 是排序依据；把最后见到的值作为 `after_sequence` 传入即可翻历史。 |

排序用 `sequence`，不要用 `created_at`。同一 `Idempotency-Key` 的重试返回
原始消息（`id`、`sequence` 都不变），重试不会产生第二条本地记录。`sender`
反映作者当前资料，不是发送时刻的快照。

### `AirwayIM::Credentials`（签名与验签工具）

本地签名 `sign` 仅面向持有 `IM_AUTH_SECRET` 的 Airway 项目方自己的服务端；
第三方平台后端不持有该密钥，必须使用 `InternalClient#mint_credential`。

| 方法 | 说明 |
| --- | --- |
| `sign(secret:, uuid:, name:, nickname: nil, avatar_url: nil, token_version: nil, ttl: 86_400, now: Time.now)` | HMAC-SHA256 签发 `im1.<payload>.<sig>` 凭证；可选字段仅在传入时写入，不会覆盖已有资料 |
| `decode(credential)` | 解出 claims（畸形输入抛 `Error`） |
| `verify_signature?(credential, secret)` | 常数时间验签，畸形输入返回 `false` |
| `expired?(credential 或 claims, now:)` | 是否已过 `exp`；无 `exp` 的凭证永不过期 |

### `AirwayIM::InternalClient`（内部铸造接口，默认 `127.0.0.1:1906`）

| 方法 | 说明 |
| --- | --- |
| `mint_credential(uuid:, name:, nickname: nil, avatar_url: nil, ttl_seconds: nil)` | 服务端对服务端铸造凭证；`ttl_seconds` 默认 86400、上限 2592000，`0` 表示无过期；返回 `MintedCredential(credential, expires_at)` |

### `AirwayIM::AdminClient`（管理后台，`/admin/api`）

首次调用自动登录（12 小时会话）；会话中途 401 会自动重登录并重试一次。

| 方法 | 说明 |
| --- | --- |
| `login` / `logout` | 显式登录 / 登出 |
| `status` | 聚合状态：用户数、在线用户、gateway/delivery 指标 |
| `users` | 已注册用户列表（含 `token_version`） |
| `revoke_user(uuid)` | 吊销该用户全部凭证并踢下线 |
| `group_conversations` | 全部群会话（含成员/消息计数） |
| `conversation_messages(conversation_id)` | 群消息审阅（升序） |
| `mark_illegal(message_id)` | 标记违规（幂等）：客户端侧内容被掩码为 `***` |

## 错误处理

所有错误抛出 `AirwayIM::Error`（`err.code` 信封业务码、`err.status` HTTP 状态
码，传输失败时 `status == 0`、`code == -1`）：

```ruby
begin
  im.send_group_message(id, "hi")
rescue AirwayIM::Error => e
  if e.auth_error?          # 10001 / 401：凭证无效或过期，等待续期或重新铸造
  elsif e.code == AirwayIM::ErrorCode::PERMISSION_DENIED      # 10005
  elsif e.code == AirwayIM::ErrorCode::CONVERSATION_NOT_FOUND # 11001
  end
end
```

完整错误码：`10000` 内部错误、`10001` 凭证无效、`10003` 请求非法、
`10005` 无权限、`11001` 会话不存在、`11002` 幂等 key 复用且请求不同。

## 消息可靠性模型

后端保证每个会话内 `sequence` 单调递增，投递是 **at-least-once**。服务端
集成的推荐做法：

1. 用 `messages(uuid, after_sequence: 上次游标)` 或 `each_message` 补齐增量；
2. 按 `msg["id"]` 幂等处理，按 `sequence` 排序；
3. 发送以 `send_group_message` 的返回值为准，重试交给 SDK（同一
   `Idempotency-Key` 保证不重发）。

客户端（小程序/浏览器）的实时接收与自动补洞由
[`airway-im-sdk-ts`](../ts/) 负责；Ruby 端轮询间隔即你的业务实时性要求。

## 线程安全

`Client` / `InternalClient` / `AdminClient` 实例可安全地在多线程间共享
（Puma 等多线程服务器）：每次请求使用独立连接，凭证刷新与登录为一次性覆盖
写。`AdminClient#login`/`logout` 与业务调用并发时请自行串行化。

## 本地开发与验证

```bash
rake test          # minitest：签名向量、信封解析、幂等重试、凭证续期、multipart……
gem build airway-im-sdk-ruby.gemspec
```

测试内置一个仅依赖标准库的 TCPServer 桩 HTTP 服务器，走真实 `Net::HTTP`
I/O。对真实后端栈（backend :1905 / internal :1906，见仓库根 README）做端到端
验证：启动栈后按「快速开始」逐步执行即可；凭证签名正确性已由 Node.js 参考
实现的跨语言向量锁定（`test/credentials_test.rb`）。

## 协议参考

- API 契约：[`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- 凭证签发：[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)
- 管理后台：[`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)

---

English version: [README.md](README.md)
