# airway-im-sdk-ruby

[Airway IM](https://github.com/daqing/airway-im-plugin) 的 Ruby SDK：把后端的
REST API、凭证签发与内部铸造接口封装成一套开箱即用的 Ruby 接口，Ruby 项目
（Rails / Sinatra / 任意 Ruby 服务）无需自己实现凭证签名、信封解析、幂等重试
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
登录成功后，**你的服务器后端**从自己的用户表取出 `(uuid, name)`，用下面两种
方式之一获得 IM 凭证，并随你自己的登录响应下发给客户端：

**方式 A：本地签名**（你的后端被授权持有 `IM_AUTH_SECRET` 时）——无网络开销：

```ruby
credential = AirwayIM::Credentials.sign(
  secret: ENV["IM_AUTH_SECRET"],
  uuid: "user-42",          # 你平台上的稳定唯一 ID
  name: "alice",            # 用户名（凭证签发时自动注册/刷新 IM 用户）
  nickname: "Alice",        # 可选，仅传入时才更新
  avatar_url: "https://…",  # 可选
  ttl: 86_400,              # 可选，默认 24 小时；nil 表示永不过期（仅限服务凭证）
)
```

**方式 B：内部铸造接口**（推荐给第三方平台）——你的后端只需持有
`IM_INTERNAL_SECRET`，签名密钥 `IM_AUTH_SECRET` 永远留在 IM 服务端；铸造出的
凭证带 `token_version`，可通过管理后台逐用户吊销：

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

两个必须分清的点（完整信任模型见
[identity.md](../../deps/im/docs/design/identity.md)）：

- 凭证永远由**你的后端**获取并下发，客户端从不向 IM 服务器索取凭证；
- 铸造接口默认只监听 `127.0.0.1:1906`，跨机部署时经内网调用，不向公网暴露。
  **没有服务器后端的纯前端无法安全接入。**

### 2. 调用 IM API

所有方法返回信封 `data`（Hash 或 Array），失败抛出 `AirwayIM::Error`：

```ruby
im = AirwayIM::Client.new(
  api_url: "https://im.example.com",
  credential: credential,
  get_credential: -> { mint_fresh_credential },  # 可选：凭证失效时自动换新并重试一次
)

im.me                                     # 当前用户资料
conversation = im.create_direct(2)        # 与用户 2 的单聊（get-or-create）
im.create_group(member_ids: [2, 3], title: "Backend Team")
im.list_groups                            # 我加入的群列表
im.conversation(conversation["id"])       # 会话类型 + 成员及角色

# 发送消息：自动生成 Idempotency-Key，网络失败自动用同一 key 重试，不会重发
im.send_message(conversation["id"], "你好", content_type: "text/plain")

# 历史 / 序列同步（掉线后从上次游标补齐，升序返回）
im.messages(conversation["id"], after_sequence: 42, limit: 100)
im.each_message(conversation["id"]).each { |msg| … }   # 自动翻页遍历全部历史

im.upload_file("/path/to/avatar.png", dir: "avatars")   # 返回 {key, url, size}
im.storage_url("avatars/202609/xxx.png")                # 下载地址
```

服务账号（机器人 / 系统通知）同样走这条路径：给机器人分配一个固定
`(uuid, name)`，铸造一个长效凭证，即可用它建会话、发消息。

## API 一览

### `AirwayIM::Client`（公共 API，默认 `:1905`）

| 方法 | 对应接口 |
| --- | --- |
| `me` | `GET /api/v1/me` |
| `list_groups` | `GET /api/v1/conversations?type=group` |
| `create_conversation(kind:, member_ids:, title: nil)` | `POST /api/v1/conversations` |
| `create_direct(other_user_id)` | 同上（direct get-or-create） |
| `create_group(member_ids:, title: nil)` | `POST /api/v1/group` |
| `conversation(uuid)` | `GET /api/v1/conversations/:uuid` |
| `add_members(uuid, member_ids)` | `POST .../members`（owner/admin，幂等） |
| `remove_member(uuid, user_id)` | `DELETE .../members/:user_id`（owner/admin） |
| `messages(uuid, after_sequence: nil, limit: nil)` | `GET .../messages`（原始分页，limit 1–200） |
| `each_message(uuid, after_sequence: 0, page_size: 100)` | 自动翻页的 Enumerator，升序遍历历史 |
| `send_message(conversation_id, content, content_type:, idempotency_key:, retries:)` | `POST /api/v1/messages` |
| `send_message_to(uuid, content, …)` | `POST /api/v1/conversations/:uuid/messages` |
| `upload_file(path 或 IO, filename:, dir:)` | `POST /api/v1/storage`（multipart） |
| `storage_url(key)` | 文件下载地址 |

构造选项：`api_url`（必填）、`credential`（必填）、`get_credential`（可选
callback，收到 `10001`/401 时自动换新凭证并重试一次；`credential` 字段随之
更新）、`timeout`（秒，默认 15）。`content_type` 支持 `text/markdown`（默认）
与 `text/plain`；内容上限 32768 字节，服务端会把 CRLF 归一为 LF。

### `AirwayIM::Credentials`（本地签名，方式 A）

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
  im.send_message(id, "hi")
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
3. 发送以 `send_message` 的返回值为准，重试交给 SDK（同一
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

- API 契约：[`deps/im/docs/api/openapi.md`](../../deps/im/docs/api/openapi.md)
- 凭证签发：[`deps/im/docs/design/identity.md`](../../deps/im/docs/design/identity.md)
- 管理后台：[`deps/im/docs/api/admin.md`](../../deps/im/docs/api/admin.md)

---

English version: [README.md](README.md)
