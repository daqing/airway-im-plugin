# airway-im-plugin（中文文档）

一个 [Airway](https://github.com/daqing/airway) 插件，打包了完整的 IM 聊天后台：Airway 项目签名凭证身份、单聊与群聊会话、基于序列号的持久化消息与断线同步、带内容审核的管理后台 API、WebSocket 网关，以及事务性 outbox 投递器。下分发，在任何启用本插件的 Airway 应用中即可独立跑起整套服务。配套的 gateway 与 delivery 服务随插件一起在 [`deps/`](../../) 下分发，在任何启用本插件的 Airway 应用中即可独立跑起整套服务。

插件通过 Airway 项目签名的 HMAC 凭证认证用户：Airway 应用对自己的 `(name, uuid)` 身份二元组
签名，插件无状态验签。英文版文档位于仓库根目录的 [`README.md`](../../../README.md)。

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

## 架构

插件采用三服务形式 —— backend / gateway / delivery：

```text
                    HTTPS (REST)                     WebSocket
  客户端 ───────────────────────────────► backend :1905
     │                                        ▲
     │  ws://gateway:1910/ws                  │ 2. 轮询 outbox
     ▼                                        │    （事件在第 1 步的
  gateway :1910 ◄──── 3. 投递 + 确认 ──── delivery :1920   事务中写入）
     │
     └─ 4. 扇出给在线接收者
```

1. 客户端通过 HTTPS 发送消息。backend 校验成员资格、分配会话 `sequence`、
   插入消息并写入 `outbox_events` —— 全部在一个数据库事务内完成。
2. delivery 投递器轮询 backend 的内部 outbox API。
3. 每个已提交的事件推送到 gateway 的内部投递端点，仅在 gateway 接受后才确认。
4. gateway 将事件扇出给本机上在线的接收者。

backend 的服务间内部 API（`/internal/v1/*` —— 第 2 步的 outbox 轮询/确认、
网关鉴权、凭证签发）由独立 listener 提供，默认只监听回环地址
（`IM_INTERNAL_ADDR`，默认 `127.0.0.1:1906`），不挂在公开端口上。

WebSocket 投递只是低延迟优化，绝不是消息的持久副本：离线客户端通过基于
sequence 的同步 API 恢复。投递语义为至少一次（at-least-once）；客户端按
`message_id` / `event_id` 去重，按 `sequence` 排序。

设计契约文档：

- [`deps/im/docs/design/identity.md`](design/identity.md) —— Airway 项目签名凭证格式、签发
  方式、客户端用法、轮换与撤销
- [`deps/im/docs/design/gateway.md`](design/gateway.md) —— 通信协议、连接生命周期、
  限制与安全模型
- [`deps/im/docs/design/delivery.md`](design/delivery.md) —— outbox 模式、扇出策略、
  顺序与幂等语义
- [`deps/im/docs/design/conversation.md`](design/conversation.md) —— 会话模型、单聊
  唯一性、成员与授权规则

## 功能介绍

**身份**

- 插件自身不带登录流程：Airway 应用使用共享密钥（`IM_AUTH_SECRET`）将用户的
  `(name, uuid)` 二元组签名成 HMAC-SHA256 凭证，插件无状态验签。
- 一张通用的 `users` 表（uuid、用户名、昵称、头像 URL、邮箱、最近活跃时间）
  是所有 IM API 的身份事实来源；用户行在凭证首次认证成功时自动注册。
- Airway 项目可以自行签发凭证（任何语言均可实现，无额外依赖），也可以调用
  `POST /internal/v1/credentials` 由 backend 代为签发。
- `GET /api/v1/me` 按凭证查询用户资料；凭证不会出现在任何 API 响应、投递
  事件或日志中。

**会话**

- 单聊与群聊共用一套模型：`direct`（恰好两名成员、规范化用户对唯一约束、
  get-or-create 语义）与 `group`（创建者成为 `owner`，成员任意，
  支持 `owner`/`admin`/`member` 角色）。
- `POST /api/v1/conversations/:uuid/members` 向已有群聊添加成员，
  `DELETE /api/v1/conversations/:uuid/members/:user_uuid` 移除成员
  （仅 owner/admin；admin 只能管理普通成员；owner 不可被移除）。两者均幂等，
  并向全体在线成员扇出 `conversation.member_added` / `conversation.member_removed`
  事件 —— 移除事件也会通知被移除的用户。
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

- 内置 Web 管理后台 `/admin/im`：用 `IM_ADMIN_USERNAME` /
  `IM_ADMIN_PASSWORD` 登录，即可在浏览器里完成全部管理操作——实时系统状态、
  用户目录与凭证撤销、群聊会话、消息审查与一键标记违规。前端是插件自带的
  内嵌 Preact bundle（TanStack Query + TanStack Table，基于 airway-ui
  组件集），无需额外部署，且遵循 `URL_PREFIX`。
- `/admin/api` 走同一套会话登录，12 小时内存会话。
- 系统状态聚合数据库计数、gateway/delivery 的实时指标与在线用户列表；
  用户列表含最近活跃时间。
- 凭证撤销：`POST /admin/api/users/:uuid/revoke` 递增用户的
  `token_version`（使 backend 签发的凭证立即失效），并踢掉 gateway 上的
  在线连接。
- 群聊会话浏览、消息查看，以及一键 `mark-illegal`：违规内容对客户端
  屏蔽为 `***`，并向在线成员扇出 `message.moderated` 事件。

**可观测性**

- gateway 与 delivery 均提供 Prometheus 风格的 `/metrics`（指标前缀
  `airway_im_gateway_*` / `airway_im_delivery_*`）和自动刷新的
  `/dashboard` HTML 页面。
- backend 的管理状态端点聚合三个服务的状态。

**Airway 集成**

- `db/migrate` 下的 Go DSL 迁移在插件被启用时随 init 注册，通过 Airway 项目二进制
  自身的 `db:migrate` 执行；借助框架的 schema 编译器支持 SQLite、MySQL 与
  PostgreSQL。
- REPL 模型（`User`）通过插件契约暴露给 Airway 项目的 REPL；文件存储 API 与任何
  Airway 应用一样可用。

## 仓库结构

| 路径 | 角色 | 默认端口 |
| --- | --- | --- |
| 仓库根目录（Go module `github.com/daqing/airway-im-plugin`） | IM 插件（包 `implugin`）：IM API、管理 API 与 Web 后台、内部 API、迁移、REPL 模型 | — |
| [`deps/im/gateway/`](../gateway/) | 独立 Go module（通过 `plugin:install` 随插件装入 Airway 项目）：WebSocket 网关 | 1910 |
| [`deps/im/delivery/`](../delivery/) | 独立 Go module（通过 `plugin:install` 随插件装入 Airway 项目）：事务性 outbox 投递器 | 1920 |
| [`deps/im/docs/`](.) | 设计文档、API 指南、OpenAPI 契约、落地页（`index.html`）、中文文档 | — |
| [`sdk/ts/`](../../../sdk/ts/) | JS/TS SDK（npm 包 `airway-im-sdk-ts`）：类型化 REST 客户端、实时网关、基于 sequence 的同步引擎，内置微信小程序与浏览器适配器 | — |

插件关键包：

| 包 | 内容 |
| --- | --- |
| `app/api/im_api` | 会话与消息端点 |
| `app/api/internal_api` | 网关鉴权、凭证签发、outbox 轮询/确认（密钥保护） |
| `app/api/me_api` | 资料查询 |
| `app/api/admin_api` | 管理后台端点：会话、用户、状态、审核 |
| `app/dashboard` | `/admin/im` Web 管理后台：内嵌 bundle（提交在 `web/dist`）、HTML 外壳、静态资产服务 |
| `app/auth` | 凭证签发/验签与用户自动注册 |
| `app/models` | `User` 模型与 REPL 注册 |
| `app/repo` | 框架 `database/sql` 连接池之上的轻量 sqlx 门面 |
| `app/utils` | 会话/消息/事件 ID 使用的 ULID 生成器 |
| `host/db/migrate` | 表结构迁移（用户、会话、成员、消息、幂等、outbox、审核） |

## 安装与启动

环境要求：Go 1.26+，本地 SQLite（文件或 `:memory:`）或 MySQL/PostgreSQL
服务。可选：Docker（gateway/delivery 各自附带 `Containerfile`，可容器化
部署）。

### 作为插件使用

在任意 Airway 应用中启用本插件 —— 用 Airway 的安装命令，或手动添加：

```bash
go run . plugin:install github.com/daqing/airway-im-plugin   # 在 Airway 应用中执行
```

本地目录安装可改用指向本仓库的 `replace` 指令。启用即向 Airway 项目的
`plugins.go` 添加 blank import `_ "github.com/daqing/airway-im-plugin"`；
import 时插件注册其路由（`/api/v1/...`、`/admin/api`、`/admin/im` Web 管理后台）、
Go DSL 迁移和 `User` REPL 模型。内部 API（`/internal/v1`）单独提供：
Airway 项目启动时插件会为它启动专用 listener（`IM_INTERNAL_ADDR`，默认
`127.0.0.1:1906`）。`plugin:install` 还会把插件的 `deps/`
目录原样复制进 Airway 项目自己的 `deps/` 目录 —— `gateway/`、`delivery/` 两个配套服务
由此到达 Airway 项目的 `deps/im/gateway` 和 `deps/im/delivery`（它们的 `go.mod.templ`
落地为 `go.mod`，已存在的文件不会被覆盖）。然后执行 Airway 项目的 `db:migrate` 创建 IM 表，
并在 Airway 项目环境中设置 `IM_AUTH_SECRET`（实时链路还需 `IM_INTERNAL_SECRET`）。

### 独立运行

任何启用了本插件的 Airway 应用都是完整的 IM backend。想单独跑起整套
服务，只需脚手架一个新的 Airway 项目、安装插件、启动三个服务：

```bash
go install github.com/daqing/airway@latest
airway new myim && cd myim
go run . plugin:install github.com/daqing/airway-im-plugin

# 编辑 .env：PORT=1905、DSN、IM_AUTH_SECRET、IM_INTERNAL_SECRET、IM_ADMIN_PASSWORD…
# （本仓库的 .env.example 列出了全部 IM 变量）
go run . db:create    # 创建数据库（驱动支持时）
go run . db:migrate   # 应用 IM 迁移
go run . server       # 启动 backend，监听 :1905
```

IM 迁移是 `db/migrate/` 下的 Go DSL 变更，通过插件包在 init 时注册，因此必须通过
**Airway 项目二进制**执行（`go run . db:migrate`），独立的 `airway` CLI 看不到它们。

再从 `plugin:install` 复制进 Airway 项目的 `deps/` 目录启动两个配套服务（三个服务
必须共享同一个 `IM_INTERNAL_SECRET`）：

```bash
(cd deps/im/gateway && BACKEND_URL=http://127.0.0.1:1906 go run .)   # gateway :1910
(cd deps/im/delivery && BACKEND_URL=http://127.0.0.1:1906 \
                        GATEWAY_URL=http://127.0.0.1:1910 go run .)  # delivery :1920
```

`BACKEND_URL` 指向 backend 的内部 API listener（`IM_INTERNAL_ADDR`，默认
`127.0.0.1:1906`）而不是公开端口 —— 配套服务只调用 `/internal/v1/*`。

也可以用 Docker 跑起整套服务：`plugin:install` 会在 Airway 项目根目录生成
`docker-compose.yml`，它构建 backend 并通过 Compose 的 `include` 引入插件的
`deps/im/docker-compose.yml`（gateway + delivery），执行
`docker compose up --build` 即可全部启动。如果 Airway 项目已有自己的
`docker-compose.yml`，安装会跳过 —— 改在你的文件里加
`include: [deps/im/docker-compose.yml]`，并确保你的应用服务名为 `backend`
且带 healthcheck。

配套服务的模块文件以 `go.mod.templ` 形式随插件分发（Go module zip 会丢弃嵌套的
`go.mod`），`plugin:install` 会在 Airway 项目中将其落地为 `go.mod`。想直接开发本仓库，
用 `replace` 指令把 Airway 项目的 `go.mod` 指向本地检出，并在本仓库执行一次
`just deps-setup` 生成 `deps/*/go.mod`，即可直接 `go run`。

### 从旧版本插件升级

旧版本插件把内部 API（`/internal/v1/*`）挂在公开端口上；现在它由独立
listener 提供（`IM_INTERNAL_ADDR`，默认 `127.0.0.1:1906`），公开端口访问
它会返回 404。在 Airway 项目 `go.mod` 中升级插件依赖后：

- **把 gateway 和 delivery 的 `BACKEND_URL` 指向内部 listener**（例如
  `http://127.0.0.1:1906`）。随插件分发的默认值已经指向新地址；只有显式
  设置过 `BACKEND_URL`（通常是 `http://<host>:1905`）的部署需要修改，
  否则实时链路会中断。
- 如果 gateway/delivery 与 backend 不在同一台机器，把 `IM_INTERNAL_ADDR`
  绑定到内网网卡（代替默认的回环地址），并确保该端口不对公网开放。

客户端、admin API 与 WebSocket 通信协议均不受影响。

## 用户认证

用户由 Airway 应用的 `(name, uuid)` 二元组标识，并签名成 HMAC 凭证。凭证首次
认证成功时，用户会自动注册到 `users` 表 —— 没有单独的"开户"步骤。完整的
凭证格式、Airway 侧签发示例（Go、Node.js、Python）与密钥轮换规则见
[`deps/im/docs/design/identity.md`](design/identity.md)。

部署拓扑里有四个角色：**第三方平台**（自己的前端：小程序或浏览器 JS；
自己的后端：通常是 PHP 或 Java）、**Airway 项目**（`airway new` 创建的 Go
应用，安装本插件后作为独立部署的微服务对外提供 IM 服务）、**本插件**
（嵌在 Airway 项目里），以及**最终用户**。第三方平台的 PHP/Java 后端嵌入不了
Go 代码，因此 Airway 项目以 API 方式对接平台后端，凭证签发发生在服务端之间：

- 用户先在第三方平台完成自己的登录（账号密码、手机验证码、`wx.login` 等）
  —— 这一步就是全系统的客户端身份认证，插件不参与。登录通过后，平台
  后端从自己的用户表取出 `(name, uuid)`，调用 Airway 项目的内部签发接口
  `POST /internal/v1/credentials`（携带 `IM_INTERNAL_SECRET`）以
  server-to-server 方式获取凭证，再随自己的登录响应下发给前端。
- 签名密钥 `IM_AUTH_SECRET` 只保存在 Airway 项目的 IM 服务端，第三方平台后端只需
  `IM_INTERNAL_SECRET`，一律通过内部签发接口获取凭证——不持有签名密钥，
  也不在平台侧本地签名。
- IM 的公开 API（`:1905`）只验签、从不给客户端签发凭证；签发端点默认只
  监听回环地址。第三方平台后端与 Airway 项目不在同一台机器时，两者必须
  处在同一个内网（同 VPC / 机房，或 VPN、专线打通），由 Airway 项目方把
  `IM_INTERNAL_ADDR` 绑定到内网网卡供其经内网地址调用，对客户端与公网
  永远不可达。客户端没有 secret，只持有并出示签好的凭证
  （REST 用 `Authorization: Bearer`，WebSocket 用首条 `auth` 命令）。
- 因此**接入方必须有自己的服务器后端**——纯前端、无服务器的小程序无法
  安全接入：客户端没有安全途径获取凭证，也没有地方存放
  `IM_INTERNAL_SECRET`。

本地开发时，最快捷的取凭证方式是内部 listener 上的 server-to-server 签发端点：

```bash
curl -sX POST http://127.0.0.1:1906/internal/v1/credentials \
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
WebSocket 事件格式）见 [`deps/im/docs/api/openapi.md`](api/openapi.md)。

```bash
# 创建群聊，把 bob（uuid 为 user-2）拉入群
curl -sX POST http://127.0.0.1:1905/api/v1/group \
  -H "Authorization: Bearer <alice 凭证>" \
  -H 'Content-Type: application/json' \
  -d '{"title":"Backend Team","member_uuids":["user-2"]}'
# → {"code":0,"data":{"id":"01M2ET18SA97XMFAG359T5TABH","kind":"group",…}}

# 创建单聊（幂等：同一对用户始终得到同一个会话）
curl -sX POST http://127.0.0.1:1905/api/v1/conversations \
  -H "Authorization: Bearer <alice 凭证>" \
  -H 'Content-Type: application/json' \
  -d '{"kind":"direct","member_uuids":["user-2"]}'

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
| `POST /api/v1/conversations/:uuid/members` | 向群聊添加成员（owner/admin） |
| `DELETE /api/v1/conversations/:uuid/members/:user_uuid` | 从群聊移除成员（owner/admin） |
| `GET /api/v1/conversations/:uuid/messages?after_sequence=N` | 历史消息 / 同步 |
| `POST /api/v1/conversations/:uuid/messages` | 向指定会话发消息 |
| `POST /api/v1/messages` | 按会话 ID 发消息 |
| `GET /admin/im` | Web 管理后台（`/admin/api` 之上的浏览器界面） |
| `/admin/api/*` | 管理 API（登录、状态、用户、审核） |
| `/internal/v1/*` | 服务间接口（网关鉴权、凭证签发、outbox、确认）—— 独立 listener（默认 `127.0.0.1:1906`）+ 密钥保护 |

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
  若凭证已过期，重连前需先向 Airway 后端重新获取凭证。
- `{"cmd":"ping"}` 返回 `{"code":0,"data":"PONG"}`；协议层 ping/pong
  每 30 秒自动运行。

端点详解：[`deps/im/docs/api/messages.md`](api/messages.md)、
[`deps/im/docs/api/me.md`](api/me.md)、[`deps/im/docs/api/admin.md`](api/admin.md)。

客户端无需手写上述协议：[`sdk/ts/`](../../../sdk/ts/) 下的 JS/TS SDK（`airway-im-sdk-ts`）把 REST API、网关协议（首帧认证、心跳、退避重连、事件去重）
与基于 sequence 的补同步封装成一个类型化的 `createIM()` 门面，并内置微信小程序
与浏览器两套平台适配器。

## 配置项参考

| 变量 | 服务 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `DSN`（或 `AIRWAY_DSN`） | backend | — | 数据库 DSN（sqlite/mysql/postgres） |
| `PORT` | backend | `1905` | HTTP 监听端口 |
| `URL_PREFIX` | backend | — | 反向代理场景下的可选子路径前缀 |
| `STORAGE_DRIVER` / `STORAGE_*` | backend | `local` | 文件存储（见 `.env.example`） |
| `IM_AUTH_SECRET` | backend、Airway 项目 | — | HMAC 凭证签名密钥；认证功能**必需** |
| `IM_AUTH_SECRET_PREVIOUS` | backend | — | 轮换期间同时接受的旧签名密钥（可选） |
| `IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD` | backend | — | 管理后台凭据 |
| `IM_GATEWAY_URL` | backend | `http://127.0.0.1:1910` | 管理后台在线状态聚合与撤销踢连使用的 gateway 基础 URL |
| `IM_INTERNAL_SECRET` | 全部 | — | `/internal/v1/*` 与网关投递端点的共享密钥；实时投递**必需** |
| `IM_INTERNAL_ADDR` | backend | `127.0.0.1:1906` | 内部 API（`/internal/v1/*`）监听地址；勿暴露到公网 |
| `ADMIN_GATEWAY_METRICS_URL` / `ADMIN_DELIVERY_METRICS_URL` | backend | 本机的 gateway/delivery | 管理状态聚合的指标端点 |
| `GATEWAY_ADDR` | gateway | `:1910` | 网关监听地址 |
| `BACKEND_URL` | gateway、delivery | `http://127.0.0.1:1906` | backend 内部 API 基础 URL |
| `GATEWAY_ALLOWED_ORIGINS` | gateway | — | 浏览器客户端的 `Origin` 白名单（逗号分隔） |
| `DELIVERY_ADDR` | delivery | `:1920` | 投递器监听地址 |
| `GATEWAY_URL` | delivery | `http://127.0.0.1:1910` | 投递推送的网关基础 URL |
| `DELIVERY_POLL_INTERVAL_MS` | delivery | `500` | outbox 轮询间隔（最小 100） |

## 开发指南

```bash
go test ./...                # 单元测试（im/admin/me/routes…）
just deps-setup              # 一次性：deps/*/go.mod.templ -> go.mod
just dashboard               # 重新构建管理后台 bundle 到 web/dist（产物需提交）
(cd deps/im/gateway && go vet . && go build .)
(cd deps/im/delivery && go vet . && go build .)
```

管理后台前端位于 `install/lib/im/app/dashboard/web/`（Preact +
TanStack Query/Table + airway-ui 组件集副本）。提交的 `web/dist` bundle
直接内嵌进插件二进制，宿主项目不需要任何 JavaScript 工具链；只有改动
管理后台本身时才需要运行 `just dashboard` 并提交产物。

测试覆盖：会话创建（单聊唯一性、成员校验）、消息持久化（幂等、
违规内容屏蔽、outbox 事件）、资料查询、管理端点与路由注册。整套服务
还做过本地端到端验证：凭证签发 → 建群 → 发消息 → outbox 轮询 →
网关推送 → WebSocket 收到事件 → outbox 确认。

