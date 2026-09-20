# airway-im-sdk-php

[Airway IM](https://github.com/daqing/airway-im-plugin) 的 PHP SDK:把后端
REST API 和凭据铸造端点封装成开箱即用的 PHP 接口,PHP 项目(Laravel /
Symfony / WordPress / 任何 PHP 服务)无需自己实现铸造调用、envelope 解析、
幂等重试或凭据自动续期。

零依赖 —— 仅依赖 PHP 8.0+ 标准库。运行时不需要任何 Composer 包,传输层基于
stream socket 实现,没有 cURL 扩展或 `allow_url_fopen` 也能工作。

**定位**:这是一个**服务端 SDK**,覆盖 PHP 后端与 IM 集成的所有场景 ——
通过内部 API 为你的用户铸造凭据、以任意用户身份调用 IM API、管理操作。
浏览器 / 小程序端的实时 WebSocket 收发,请搭配 JS/TS SDK
([`airway-im-sdk-ts`](../ts/),含同步引擎);需要监听新消息的 PHP 侧
(机器人、通知)直接轮询基于 sequence 的同步 API 即可(见下文
`eachMessage`)。

## 安装

使用 Composer:

```bash
composer require daqing/airway-im-sdk-php
```

Composer 的 PSR-4 自动加载会自动发现这些类。不用 Composer 的话,直接引入
自带的 autoloader:

```php
require '/path/to/airway-im-sdk-php/src/autoload.php';
```

## 快速上手

### 1. 为你的用户获取凭据

SDK 不包含登录逻辑。用户先在你的平台上登录(密码、短信验证码……);登录成功后,
**你的服务端**从自己的用户表读出 `(uuid, name)`,通过服务端到服务端的方式调用
IM 服务的**内部铸造端点**换取 IM 凭据,随你自己的登录响应一起返回给客户端。
你的后端只持有 `IM_INTERNAL_SECRET`;签名密钥 `IM_AUTH_SECRET` 永远不出 IM
服务器,且铸造出的凭据带有 `token_version`,可通过管理 API 撤销:

```php
use AirwayIM\InternalClient;

$internal = new InternalClient(
    internalUrl: 'http://127.0.0.1:1906',   // 内部监听地址,仅限私有网络
    internalSecret: getenv('IM_INTERNAL_SECRET'),
);

$minted = $internal->mintCredential(
    uuid: 'user-42',
    name: 'alice',
    nickname: 'Alice',
    ttlSeconds: 86400,
);
$credential = $minted->credential();        // "im1.…"
$minted->expiresAt();                       // ISO8601 字符串;ttlSeconds 为 0 时是 null
```

第三方平台后端**只能**通过内部铸造端点获取凭据:本地签名(持有
`IM_AUTH_SECRET` 自己算 HMAC)保留给 Airway 项目自己的后端 —— 平台侧代码
永远不持有签名密钥。

必须分清两件事(完整信任模型见
[identity.md](../../../deps/im/docs/design/identity.md)):

- 凭据始终由**你的后端**获取并下发;客户端从不向 IM 服务器索取凭据。
- 铸造端点默认监听 `127.0.0.1:1906`;跨主机时走私有网络,绝不公开暴露。
  **没有服务端后端的纯前端无法安全集成。**

### 2. 调用 IM API

所有方法返回 envelope 的 `data`(数组或 null),失败时抛出
`AirwayIM\Error`。会话获取类方法返回 `AirwayIM\DirectConversation` /
`AirwayIM\GroupConversation` 会话对象 —— 每个会话一个对象,携带 `->id` 与
`->kind`(群会话另有 `->title`),并把 `send` / `listMessages` /
`eachMessage` 收拢到自身。消息响应返回 `AirwayIM\Message`,一个 JSON 对象
包装 —— `$message["id"]`、`$message->id`、`$message->sender->uuid` 三种写法
都行:

```php
use AirwayIM\Client;

$im = new Client(
    apiUrl: 'https://im.example.com',
    credential: $credential,
    getCredential: fn () => mintFreshCredential(), // 可选:401/10001 时自动换取新凭据并重试一次
);

$direct = $im->createDirect('user-2');      // DirectConversation 会话对象(get-or-create)
$im->getDirect('user-2');       // 与 "user-2" 的已有会话对象,没有则 null(只读)
$group = $im->createGroup(memberUUIDs: ['user-2', 'user-3'], title: 'Backend Team');
$im->listGroups();                          // 我所在的群
$group->details();                           // 会话类型 + 成员及角色
$im->openConversation($group->id);          // 按任意会话 id 打开会话对象

// 发消息:Idempotency-Key 自动生成,并在网络失败重试间复用,消息不会重复
$direct->send('你好');
$message = $im->sendDirectMessage('user-2', '你好'); // 一次调用:get-or-create 单聊会话,然后发送
$message->sequence;                         // Message 支持属性读取;$message['sequence'] 也可以

$group->addMembers(['user-4']);             // 群专属操作直接在会话对象上
$group->removeMembers(['user-4']);
$group->details();                          // 成员及角色,实时来自 API

// 历史 / sequence 同步(离线后从上一个游标补齐,升序返回)
$im->listMessages($direct->id, afterSequence: 42, limit: 100);
foreach ($im->eachMessage($direct->id) as $message) { … } // 自动翻页遍历全部历史

$im->uploadFile('/path/to/avatar.png', dir: 'avatars');   // 返回 {key, url, size}
$im->storageUrl('avatars/202609/xxx.png');                // 下载 URL
```

服务账号(机器人 / 系统通知)走同样的路径:给机器人一个固定的
`(uuid, name)`,铸造一个长效凭据,用它建会话、发消息。

## API 参考

### `AirwayIM\Client`(公开 API,默认 `:1905`)

方法名与 TS SDK 一一对应(两边都是 camelCase):`createDirect` ↔
`createDirect`、`getDirect` ↔ `getDirect`、
`createGroup` ↔ `createGroup`、`listMessages` ↔ `listMessages` 等。会话对象同样齐备 ——
`AirwayIM\DirectConversation` / `AirwayIM\GroupConversation` 提供 `send` /
`listMessages` / `eachMessage`(群会话另有 `addMembers` / `removeMembers` /
`details`),以及 `openConversation(id)`;缺的只是实时层——TS 会话对象上的
`on("message")` 订阅、WebSocket 网关和同步引擎,PHP SDK 以轮询
`eachMessage` 代替。

| 方法 | 端点 |
| --- | --- |
| `me()` | `GET /api/v1/me` |
| `listGroups()` | `GET /api/v1/conversations?type=group` |
| `createDirect(otherUserUUID)` | 同上(direct get-or-create),返回 `DirectConversation` 会话对象 |
| `getDirect(otherUserUUID)` | `GET /api/v1/conversations/direct/:user_uuid`(返回会话对象;没有时 null) |
| `createGroup(title: null, memberUUIDs)` | `POST /api/v1/group`(返回 `GroupConversation` 会话对象) |
| `openConversation(conversationId)` | `GET /api/v1/conversations/:uuid`,以会话对象返回(从详情解析类型/标题) |
| `addMembers(conversationId, memberUUIDs)` | `POST .../members`(群主/管理员,幂等) |
| `removeMembers(conversationId, userUUIDs)` | `DELETE .../members/:user_uuid`(群主/管理员) |
| `listMessages(conversationId, afterSequence: null, limit: null)` | `GET .../messages`(原始分页,limit 1–200) |
| `eachMessage(conversationId, afterSequence: 0, pageSize: 100)` | 自动翻页的生成器,按升序遍历历史 |
| `sendGroupMessage(conversationId, content, contentType:, idempotencyKey:, retries:)` | `POST /api/v1/messages` |
| `sendDirectMessage(otherUserUUID, content, …)` | get-or-create 单聊会话后 `POST /api/v1/messages` |
| `uploadFile(path 或 stream, filename:, dir:)` | `POST /api/v1/storage`(multipart) |
| `storageUrl(key)` | 文件下载 URL |

构造参数:`apiUrl`(必填)、`credential`(必填)、`getCredential`(可选回调 ——
遇到 `10001`/401 时自动获取新凭据并重试一次,`credential` 属性随之更新)、
`timeout`(秒,默认 15)。`contentType` 接受 `text/markdown`(默认)和
`text/plain`;内容上限 32768 字节,服务端会把 CRLF 归一化为 LF。

### 会话对象

`createDirect` 和 `getDirect` 返回 `AirwayIM\DirectConversation`;
`createGroup` 和 `openConversation` 通过详情查询解析为
`AirwayIM\GroupConversation`(或 `DirectConversation`)。每个会话对象都携带
`->id` 与 `->kind`,并把会话 API 收拢到自身:

| 会话对象方法 | 委托到 |
| --- | --- |
| `send(content, contentType:, idempotencyKey:, retries:)` | `POST /api/v1/messages` |
| `listMessages(afterSequence:, limit:)` | `listMessages(id, …)` |
| `eachMessage(afterSequence: 0, pageSize: 100)` | `eachMessage(id, …)` |
| `addMembers(memberUUIDs)` —— 仅群 | `addMembers(id, memberUUIDs)` |
| `removeMembers(userUUIDs)` —— 仅群 | `removeMembers(id, userUUIDs)` |
| `details()` | `GET /api/v1/conversations/:uuid` |

`$group->title` 携带创建时的标题(未传或按 id 打开时为 `null`);`details()`
返回实时值。

### Message 对象

`sendGroupMessage`、`sendDirectMessage`、`listMessages`、`eachMessage` 返回
`AirwayIM\Message` 对象 —— 支持属性读取的 JSON 对象,`$message["id"]`、
`$message->id`、`$message->sender->uuid` 都可以,`json_encode($message)`
能序列化回 API 载荷:

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string,26 位 ULID | 服务端生成的全局唯一消息标识;去重的稳定键。 |
| `conversation_id` | string,26 位 ULID | 消息所属会话;按它把消息路由到对应聊天窗口。 |
| `sender.uuid` | string | 作者的稳定身份 uuid(API 与事件中身份只有 uuid)。 |
| `sender.username` | string | 宿主分配的账号句柄,账号生命周期内稳定。 |
| `sender.nickname` | string \| null | 首选展示名;为 null 时回退到 `username`。 |
| `sender.avatar_url` | string \| null | 头像 URL;为 null 时渲染占位图。 |
| `content` | string | 消息正文,1–32768 UTF-8 字节,服务端把 CRLF 归一化为 LF。被审核的消息读回字面 `***`。 |
| `content_type` | string | `text/markdown`(默认)或 `text/plain` —— 决定如何渲染 `content`。 |
| `created_at` | string,RFC 3339 UTC | 服务端提交时间戳;展示时换算成查看者时区。 |
| `sequence` | integer ≥ 1 | 消息在会话中的位置,提交时分配。`(conversation_id, sequence)` 是全序;把上次见到的值作为 `afterSequence` 传入即可翻页历史。 |

排序用 `sequence`,不要用 `created_at`。带同一 `Idempotency-Key` 的重试会
返回原消息(相同 `id` 和 `sequence`),重试不会产生第二条本地记录。
`sender` 反映作者当前资料,不是发送时的快照。

### `AirwayIM\Credentials`(签名与校验助手)

`sign` 本地签名只面向 Airway 项目自己一侧、可信持有 `IM_AUTH_SECRET` 的
场景;第三方平台后端永远不持有该密钥,必须用
`InternalClient::mintCredential`。

| 方法 | 说明 |
| --- | --- |
| `sign(secret, uuid, name, nickname: null, avatarUrl: null, tokenVersion: null, ttl: 86400, now: null)` | 签发 `im1.<payload>.<sig>` HMAC-SHA256 凭据;可选字段只在传入时写入,不会覆盖已有资料;`now` 是可注入的 unix 秒时钟 |
| `decode(credential)` | 返回 claims(非法输入抛 `Error`) |
| `verifySignature(credential, secret)` | 常数时间校验,非法输入返回 `false` |
| `expired(credential 或 claims, now: null)` | `exp` 是否已过;没有 `exp` 的凭据永不过期 |

### `AirwayIM\InternalClient`(内部铸造端点,默认 `127.0.0.1:1906`)

| 方法 | 说明 |
| --- | --- |
| `mintCredential(uuid, name, nickname: null, avatarUrl: null, ttlSeconds: null)` | 服务端到服务端铸造凭据;`ttlSeconds` 默认 86400,上限 2592000,`0` 表示不过期;返回 `MintedCredential`(`credential()`、`expiresAt()`) |

### `AirwayIM\AdminClient`(管理台,`/admin/api`)

首次调用自动登录(12 小时会话);会话中出现 401 会自动重新登录并重试一次。

| 方法 | 说明 |
| --- | --- |
| `login` / `logout` | 显式登录 / 登出 |
| `status` | 聚合状态:用户数、在线用户、gateway/delivery 指标 |
| `users` | 已注册用户(含 `token_version`) |
| `revokeUser(uuid)` | 撤销用户全部凭据并踢掉其连接 |
| `groupConversations` | 所有群会话(含成员/消息数) |
| `conversationMessages(conversationId)` | 群消息审查(升序) |
| `markIllegal(messageId)` | 标记违规(幂等):对客户端遮蔽为 `***` |

## 错误处理

所有错误抛出 `AirwayIM\Error`(`getCode()` —— envelope 业务码,`->status`
—— HTTP 状态码;传输失败时 `status == 0` 且 `code == -1`):

```php
use AirwayIM\Error;
use AirwayIM\ErrorCode;

try {
    $im->sendGroupMessage($id, 'hi');
} catch (Error $e) {
    if ($e->authError()) {                                           // 10001 / 401:凭据无效或过期 —— 换新或重铸
    } elseif ($e->getCode() === ErrorCode::PERMISSION_DENIED) {      // 10005
    } elseif ($e->getCode() === ErrorCode::CONVERSATION_NOT_FOUND) { // 11001
    }
}
```

完整错误码:`10000` 内部错误,`10001` 凭据无效,`10003` 请求非法,`10005`
无权限,`11001` 会话不存在,`11002` 幂等键被不同请求复用。

## 消息可靠性模型

后端保证每会话单调递增的 `sequence`;投递是 **at-least-once**。服务端
集成的推荐做法:

1. 用 `messages($uuid, afterSequence: $lastCursor)` 或 `eachMessage` 补齐;
2. 按 `$message['id']` 幂等处理,按 `sequence` 排序;
3. 以 `sendGroupMessage` 的返回值为准 —— 重试由 SDK 处理(同一个
   `Idempotency-Key` 保证不重复)。

端用户客户端(小程序 / 浏览器)的实时接收与自动补洞属于
[`airway-im-sdk-ts`](../ts/);PHP 轮询间隔就是你的业务延迟要求。

## 并发

`Client` / `InternalClient` / `AdminClient` 实例可以跨请求、跨 worker 任务
复用:每个请求使用独立连接,凭据刷新 / 登录都是一次性覆盖。若
`AdminClient::login` / `logout` 与业务调用并发执行,请自行做串行化。

## 本地开发与验证

```bash
php test/run.php   # 纯标准库测试 runner:签名向量、envelope 解析、幂等重试、凭据续期、multipart、chunked 响应……
```

测试自带一个基于真实 socket 的数据驱动 stub HTTP server(无 PHPUnit、无
扩展),覆盖真实的请求/响应 I/O,包括传输重试与读超时。要对真实栈做端到端
验证(backend :1905 / internal :1906,见仓库根 README):启动栈后运行
`php test/e2e.php`,或按"快速上手"逐步走通;凭据签名正确性由 Node.js 参考
实现的跨语言向量锁定(`test/credentials_test.php`)。

## 协议参考

- API 契约:[`deps/im/docs/api/openapi.md`](../../../deps/im/docs/api/openapi.md)
- 凭据签发:[`deps/im/docs/design/identity.md`](../../../deps/im/docs/design/identity.md)
- 管理台:[`deps/im/docs/api/admin.md`](../../../deps/im/docs/api/admin.md)

---

English version: [README.md](README.md)
