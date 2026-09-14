# airway-im-plugin（中文文档）

一个 [Airway](https://github.com/daqing/airway) 应用，将完整的 IM 聊天后台打包为插件：宿主签名凭证身份、单聊与群聊会话、基于序列号的持久化消息与断线同步、带内容审核的管理后台 API、WebSocket 网关，以及事务性 outbox 投递器。

IM 实现移植自 KongChat 产品代码库，并适配到当前版本的 Airway 框架。参考项目特有的
GitHub OAuth 登录被有意去掉 —— 它是产品专属逻辑，与 IM 核心无关 —— 插件改为通过
宿主签名的 HMAC 凭证认证用户：宿主应用对自己的 `(name, uuid)` 身份二元组签名，插件
无状态验签（差异见[与参考实现的差异](#与参考实现的差异)）。英文版文档位于仓库根目录的
[`README.md`](../README.md)。

## 目录

- [架构](#架构)
- [功能介绍](#功能介绍)
- [仓库结构](#仓库结构)
- [安装与启动](#安装与启动)
- [用户认证](#用户认证)
- [使用 IM API](#使用-im-api)
- [建立 WebSocket 连接](#建立-websocket-连接)
- [配置项参考](#配置项参考)
- [开发指南](#开发指南)
- [与参考实现的差异](#与参考实现的差异)

## 架构

插件采用与参考项目一致的三服务形式 —— backend / gateway / delivery：

```text
                    HTTPS (REST)                     WebSocket
  客户端 ───────────────────────────────► backend :1905
     │                                        │    ▲
     │  ws://gateway:1910/ws                  │    │ 4. 轮询 outbox、
     ▼                                        │    │    推送、确认
  gateway :1910 ◄───── 3. 投递命令 ──────── delivery :1920
     ▲                                        │
     └──────────── 2. outbox 事件 ───────────┘
                 （与消息在同一个事务中写入）
```

1. 客户端通过 HTTPS 发送消息。backend 校验成员资格、分配会话 `sequence`、
   插入消息并写入 `outbox_events` —— 全部在一个数据库事务内完成。
2. delivery 投递器轮询 backend 的内部 outbox API。
3. 每个已提交的事件推送到 gateway 的内部投递端点，仅在 gateway 接受后才确认。
4. gateway 将事件扇出给本机上在线的接收者。

WebSocket 投递只是低延迟优化，绝不是消息的持久副本：离线客户端通过基于
sequence 的同步 API 恢复。投递语义为至少一次（at-least-once）；客户端按
`message_id` / `event_id` 去重，按 `sequence` 排序。

设计契约文档：

- [`docs/design/identity.md`](design/identity.md) —— 宿主签名凭证格式、签发
  方式、客户端用法、轮换与撤销
- [`docs/design/gateway.md`](design/gateway.md) —— 通信协议、连接生命周期、
  限制与安全模型
- [`docs/design/delivery.md`](design/delivery.md) —— outbox 模式、扇出策略、
  顺序与幂等语义
- [`docs/design/conversation.md`](design/conversation.md) —— 会话模型、单聊
  唯一性、成员与授权规则

## 功能介绍

**身份**

- 插件自身不带登录流程：宿主应用使用共享密钥（`IM_AUTH_SECRET`）将用户的
  `(name, uuid)` 二元组签名成 HMAC-SHA256 凭证，插件无状态验签。
- 一张通用的 `users` 表（uuid、用户名、昵称、头像 URL、邮箱、最近活跃时间）
  是所有 IM API 的身份事实来源；用户行在凭证首次认证成功时自动注册。
- 宿主可以自行签发凭证（任何语言均可实现，无额外依赖），也可以调用
  `POST /internal/v1/credentials` 由 backend 代为签发。
- `GET /api/v1/me` 按凭证查询用户资料；凭证不会出现在任何 API 响应、投递
  事件或日志中。

**会话**

- 单聊与群聊共用一套模型：`direct`（恰好两名成员、规范化用户对唯一约束、
  get-or-create 语义）与 `group`（创建者成为 `owner`，成员任意，
  支持 `owner`/`admin`/`member` 角色）。
- 会话 ID 为不透明的 26 位 ULID；成员历史通过 `left_at` 保留，不删除行。

**消息**

- `POST /api/v1/messages` 及嵌套在会话下的变体，支持 `text/markdown`
  （默认）或 `text/plain`，单条上限 32 KiB。
- 可选 `Idempotency-Key` 请求头：相同键 + 相同请求体重放返回原始消息；
  相同键 + 不同请求体返回 409。
- 会话内单调递增的 `sequence` 在事务中分配；重连后通过
  `GET .../messages?after_sequence=N` 同步错过的消息，客户端据此检测空洞。

**实时投递**

- 独立部署的 WebSocket 网关，采用 `{"cmd":"auth","opts":["<凭证>"]}`
  首命令认证协议、10 秒认证期限、30s/40s ping/pong、64 KiB 帧上限、
  每连接 256 条的有界发送队列（慢消费者被断开，通过同步恢复）。
- 支持同一用户多设备同时在线；每个连接单写循环。

**管理与审核**

- `/admin/api` 会话登录（`IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD`），
  12 小时内存会话。
- 系统状态聚合数据库计数、gateway/delivery 的实时指标与在线用户列表；
  用户列表含最近活跃时间。
- 凭证撤销：`POST /admin/api/users/:uuid/revoke` 递增用户的
  `token_version`（使 backend 签发的凭证立即失效），并踢掉 gateway 上的
  在线连接。
- 群聊会话浏览、消息查看，以及一键 `mark-illegal`：违规内容对客户端
  屏蔽为 `***`，并向在线成员扇出 `message.moderated` 事件。

**可观测性**

- gateway 与 delivery 均提供 Prometheus 风格的 `/metrics` 和自动刷新的
  `/dashboard` HTML 页面。
- backend 的管理状态端点聚合三个服务的状态。

**Airway 集成**

- `db/migrate` 下的 Go DSL 迁移在 init 时注册，通过项目二进制自身的 CLI
  执行；借助框架的 schema 编译器支持 SQLite、MySQL 与 PostgreSQL。
- 与任何 Airway 应用一样，REPL 模型（`User`）与文件存储 API 可用。

## 仓库结构

| 路径 | 角色 | 默认端口 |
| --- | --- | --- |
| 仓库根目录（Go module `github.com/daqing/airway-im-plugin`） | backend API 服务：IM API、管理 API、内部 API、迁移 | 1905 |
| [`gateway/`](../gateway/) | 独立 Go module：WebSocket 网关 | 1910 |
| [`delivery/`](../delivery/) | 独立 Go module：事务性 outbox 投递器 | 1920 |
| [`docs/`](.) | 设计文档、API 指南、OpenAPI 契约、中文文档 | — |

backend 关键包：

| 包 | 内容 |
| --- | --- |
| `app/api/im_api` | 会话与消息端点 |
| `app/api/internal_api` | 网关鉴权、凭证签发、outbox 轮询/确认（密钥保护） |
| `app/api/me_api` | 资料查询 |
| `app/api/admin_api` | 管理后台端点：会话、用户、状态、审核 |
| `app/auth` | 凭证签发/验签与用户自动注册 |
| `app/models` | `User` 模型与 REPL 注册 |
| `app/repo` | 框架 `database/sql` 连接池之上的轻量 sqlx 门面 |
| `app/utils` | 会话/消息/事件 ID 使用的 ULID 生成器 |
| `db/migrate` | 表结构迁移（用户、会话、成员、消息、幂等、outbox、审核） |

## 安装与启动

环境要求：Go 1.26+，本地 SQLite（文件或 `:memory:`）或 MySQL/PostgreSQL
服务。可选：`overmind` + `tmux`（用于 `just dev`）、Docker（用于 compose）。

```bash
cp .env.example .env    # 然后编辑 DSN、IM_AUTH_SECRET、IM_INTERNAL_SECRET、IM_ADMIN_PASSWORD…
go run . db:create      # 创建数据库（驱动支持时）
go run . db:migrate     # 应用 IM 迁移
go run .                # 启动 backend，监听 :1905（或：`airway server`）
```

IM 迁移是 `db/migrate/` 下的 Go DSL 变更，在 init 时注册，因此必须通过
**本项目二进制**执行（`go run . db:migrate`），独立的 `airway` CLI 看不到它们。

本地同时运行三个服务（必须共享同一个 `IM_INTERNAL_SECRET`）：

```bash
just dev                                                          # 通过 overmind

# 或手动启动：
go run .                                                          # backend :1905
(cd gateway && BACKEND_URL=http://127.0.0.1:1905 go run .)        # gateway :1910
(cd delivery && BACKEND_URL=http://127.0.0.1:1905 \
                 GATEWAY_URL=http://127.0.0.1:1910 go run .)      # delivery :1920
```

或用 Docker Compose 一键起整套服务（启动时自动迁移）：

```bash
docker compose up --build
```

## 用户认证

用户由宿主应用的 `(name, uuid)` 二元组标识，并签名成 HMAC 凭证。凭证首次
认证成功时，用户会自动注册到 `users` 表 —— 没有单独的"开户"步骤。完整的
凭证格式、宿主侧签发示例（Go、Node.js、Python）与密钥轮换规则见
[`docs/design/identity.md`](design/identity.md)。

本地开发时，最快捷的取凭证方式是 server-to-server 签发端点：

```bash
curl -sX POST http://127.0.0.1:1905/internal/v1/credentials \
  -H "X-IM-Internal-Secret: <IM_INTERNAL_SECRET>" \
  -H 'Content-Type: application/json' \
  -d '{"uuid":"user-1","name":"alice","nickname":"Alice"}'
# → {"code":0,"data":{"credential":"im1.…","expires_at":"…"},"message":null}
```

再用不同的 `uuid`/`name`（例如 bob）签发第二个凭证，就可以用返回的凭证调用
IM API。凭证默认 24 小时过期，按需重新签发即可。

## 使用 IM API

所有端点返回统一信封 `{"code":0,"data":…,"message":null}`，并通过
`Authorization: Bearer <凭证>` 认证。完整的客户端契约（含错误码与
WebSocket 事件格式）见 [`docs/api/openapi.md`](api/openapi.md)。

```bash
# 创建群聊，拉用户 2（bob）入群
curl -sX POST http://127.0.0.1:1905/api/v1/group \
  -H "Authorization: Bearer <alice 凭证>" \
  -H 'Content-Type: application/json' \
  -d '{"title":"Backend Team","member_ids":[2]}'
# → {"code":0,"data":{"id":"01M2ET18SA97XMFAG359T5TABH","kind":"group",…}}

# 创建单聊（幂等：同一对用户始终得到同一个会话）
curl -sX POST http://127.0.0.1:1905/api/v1/conversations \
  -H "Authorization: Bearer <alice 凭证>" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"direct","member_ids":[2]}'

# 发送消息 —— 携带相同 Idempotency-Key 可安全重试
curl -sX POST http://127.0.0.1:1905/api/v1/messages \
  -H "Authorization: Bearer <alice 凭证>" \
  -H "Idempotency-Key: 018f7d62-60d1-7d24-bfe1-3b4410f09a0a" \
  -d '{"conversation_id":"01M2ET18…","content":"Hello **team**!","content_type":"text/markdown"}'
# → {"code":0,"data":{"sequence":1,"sender":{…},…}}

# 列出我所在的群聊
curl -s "http://127.0.0.1:1905/api/v1/conversations?type=group" \
  -H "Authorization: Bearer <alice 凭证>"

# 查看会话详情（类型 + 在线成员及角色）
curl -s http://127.0.0.1:1905/api/v1/conversations/<会话ID> \
  -H "Authorization: Bearer <alice 凭证>"

# 离线/断线后同步错过的消息
curl -s "http://127.0.0.1:1905/api/v1/conversations/<会话ID>/messages?after_sequence=42" \
  -H "Authorization: Bearer <alice 凭证>"
```

HTTP API 一览：

| 方法与路径 | 用途 |
| --- | --- |
| `GET /api/v1/me` | 当前用户资料 |
| `POST /api/v1/group` | 创建群聊 |
| `GET /api/v1/conversations?type=group` | 列出我的群聊 |
| `POST /api/v1/conversations` | 创建单聊/群聊 |
| `GET /api/v1/conversations/:uuid` | 会话详情与成员 |
| `GET /api/v1/conversations/:uuid/messages?after_sequence=N` | 历史消息 / 同步 |
| `POST /api/v1/conversations/:uuid/messages` | 向指定会话发消息 |
| `POST /api/v1/messages` | 按会话 ID 发消息 |
| `/admin/api/*` | 管理后台（登录、状态、用户、审核） |
| `/internal/v1/*` | 服务间接口（网关鉴权、凭证签发、outbox、确认）—— 密钥保护 |

## 建立 WebSocket 连接

客户端连接 `ws://127.0.0.1:1910/ws`，并必须在**第一条**应用消息中完成认证：

```json
{"cmd":"auth","opts":["<凭证>"]}
```

- 成功：返回 `{"code":0,"data":"OK","message":null}`；连接随即绑定到该
  用户，并可接收服务端主动推送的事件帧，例如
  `{"event":"message.created","message_id":…,"conversation_id":…,"sequence":…}`。
- 失败/超时：返回错误信封后以关闭码 `1008` 断开。客户端应退避重连，
  然后通过 `after_sequence` 补拉消息 —— 切勿依赖 socket 恢复错过的消息。
  若凭证已过期，重连前需先向宿主后端重新获取凭证。
- `{"cmd":"ping"}` 返回 `{"code":0,"data":"PONG"}`；协议层 ping/pong
  每 30 秒自动运行。

端点详解：[`docs/api/messages.md`](api/messages.md)、
[`docs/api/me.md`](api/me.md)、[`docs/api/admin.md`](api/admin.md)。

## 配置项参考

| 变量 | 服务 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `DSN`（或 `AIRWAY_DSN`） | backend | — | 数据库 DSN（sqlite/mysql/postgres） |
| `PORT` | backend | `1905` | HTTP 监听端口 |
| `URL_PREFIX` | backend | — | 反向代理场景下的可选子路径前缀 |
| `STORAGE_DRIVER` / `STORAGE_*` | backend | `local` | 文件存储（见 `.env.example`） |
| `IM_AUTH_SECRET` | backend、宿主 | — | HMAC 凭证签名密钥；认证功能**必需** |
| `IM_AUTH_SECRET_PREVIOUS` | backend | — | 轮换期间同时接受的旧签名密钥（可选） |
| `IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD` | backend | — | 管理后台凭据 |
| `IM_GATEWAY_URL` | backend | `http://127.0.0.1:1910` | 管理后台在线状态聚合与撤销踢连使用的 gateway 基础 URL |
| `IM_INTERNAL_SECRET` | 全部 | — | `/internal/v1/*` 与网关投递端点的共享密钥；实时投递**必需** |
| `ADMIN_GATEWAY_METRICS_URL` / `ADMIN_DELIVERY_METRICS_URL` | backend | 本机的 gateway/delivery | 管理状态聚合的指标端点 |
| `GATEWAY_ADDR` | gateway | `:1910` | 网关监听地址 |
| `BACKEND_URL` | gateway、delivery | `http://127.0.0.1:1905` | backend 基础 URL |
| `GATEWAY_ALLOWED_ORIGINS` | gateway | — | 浏览器客户端的 `Origin` 白名单（逗号分隔） |
| `DELIVERY_ADDR` | delivery | `:1920` | 投递器监听地址 |
| `GATEWAY_URL` | delivery | `http://127.0.0.1:1910` | 投递推送的网关基础 URL |
| `DELIVERY_POLL_INTERVAL_MS` | delivery | `500` | outbox 轮询间隔（最小 100） |

## 开发指南

```bash
go test ./...        # backend 单元测试（im/admin/me/routes…）
(cd gateway && go vet . && go build .)
(cd delivery && go vet . && go build .)
just dev             # 同时运行 backend + gateway + delivery + templ 监听
just generate        # 编辑 .templ 视图后重新生成 *_templ.go
go run . repl        # 交互式 REPL（含本项目模型）
go run . db:rollback # 回滚最近一次迁移
```

backend 测试覆盖：会话创建（单聊唯一性、成员校验）、消息持久化（幂等、
违规内容屏蔽、outbox 事件）、资料查询、管理端点与路由注册。移植实现
还做过本地端到端验证：凭证签发 → 建群 → 发消息 → outbox 轮询 →
网关推送 → WebSocket 收到事件 → outbox 确认。

## 与参考实现的差异

IM 业务逻辑是对参考 backend 的忠实移植；以下差异仅源于当前 Airway 框架
（v0.7.1）或插件化打包的需要：

- **去掉 OAuth 的身份模型。** 参考项目通过 GitHub OAuth 流程和
  `github_users` 表认证 GitHub 用户。两者都作为产品专属逻辑被移除：插件
  保留一张通用 `users` 表（uuid、用户名、展示字段），通过宿主签名的
  HMAC 凭证认证，并在首次认证时自动注册用户。其余部分 —— 会话级授权、
  通信协议 —— 与参考完全一致。
- **数据访问层。** 参考项目携带的是老版 Airway 分支（基于 sqlx 的 repo
  层）。本插件使用上游 Airway（纯 `database/sql`）并在 `app/repo` 增加了
  轻量 sqlx 门面，使移植的业务代码保持原有查询写法。
- **ULID 辅助函数。** 该框架版本没有 `utils.NewULID`，现位于 `app/utils`。
- **迁移。** 保持 Go DSL 形式（IM 表结构与参考一致），但文件名不带数字
  前缀，避免框架 SQL 优先 CLI 的告警；迁移通过 init 注册，由项目二进制执行。
- **命名。** 产品专有的 `KONGCHAT_*` 环境变量改为 `IM_AUTH_SECRET`、
  `IM_INTERNAL_SECRET`、`IM_ADMIN_USERNAME`、`IM_ADMIN_PASSWORD`；内部请求头
  `X-KongChat-Internal-Secret` 改为 `X-IM-Internal-Secret`；
  gateway/delivery 指标前缀为 `airway_im_gateway_*` / `airway_im_delivery_*`。
  客户端可见的通信协议保持不变。
- **URL 前缀支持。** 服务间内部 API 同时挂载在无前缀的 internal 路由上，
  因此即使公共路由运行在 `URL_PREFIX` 之下，gateway/delivery 仍可直达
  backend。
- **移除脚手架。** 插件原有的 demo WebSocket 广播（`app/websocket`、
  `/ws` 原型）已删除；独立部署的 gateway 服务才是真正的实时通道。
