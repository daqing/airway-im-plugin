# Airway IM client demo (TypeScript)

A dependency-free TypeScript client for the Airway IM plugin, serving as an
end-to-end proof of the plugin's chat backend: a multi-user group chat TUI
(`src/main.ts`) plus a scripted multi-user verification (`src/demo.ts`).

Runs on Node.js ≥ 22 using only built-ins — `fetch` for the REST API, the
native `WebSocket` global for the gateway, and Node's native TypeScript
type-stripping (no build step). `typescript` / `@types/node` are dev-only,
used by `pnpm typecheck`.

## What it exercises

- **Identity** — credential minting via `POST /internal/v1/credentials`
  (dev convenience), auto-registration, `GET /api/v1/me`
- **Conversations** — group creation with roles, group listing, member
  inspection via `GET /api/v1/conversations/:uuid`
- **Messages** — `POST /api/v1/messages` with `Idempotency-Key`,
  `text/markdown` content
- **Realtime** — gateway WebSocket auth-first-frame protocol, `message.created`
  fan-out to every member, dedupe by `event_id`/`message_id`, app-level ping,
  reconnect with backoff
- **Sync** — `after_sequence` catch-up after reconnect and for offline gaps;
  `message.moderated` reload

## Prerequisites

The three backend services must be running (see the repo root README,
"Running standalone"):

| Service | Port | Notes |
| --- | --- | --- |
| backend | 1905 | any Airway application with the plugin enabled |
| gateway | 1910 | `deps/im/gateway` |
| delivery | 1920 | `deps/im/delivery` |

All three must share `IM_INTERNAL_SECRET`; the backend additionally needs
`IM_AUTH_SECRET`.

## Scripted proof: `pnpm demo`

`src/demo.ts` spins up three users (alice, bob, carol) in one process — each
with a minted credential, an HTTP client, and its own gateway connection — and
verifies:

1. credential minting + user auto-registration
2. group creation and role assignment (owner/member)
3. gateway authentication of three concurrent connections
4. realtime fan-out: every member receives all messages
5. idempotent resend: same key replays the same message, delivered once
6. offline catch-up: a disconnected client resyncs via `after_sequence`
7. identical, gapless message history for every member

```bash
pnpm install        # dev-only, for typecheck
IM_INTERNAL_SECRET=<backend secret> pnpm demo
```

Exits non-zero if any check fails.

## Interactive group chat TUI: `pnpm chat`

```bash
# alice creates a group with user ids 2 and 3
IM_INTERNAL_SECRET=<backend secret> \
  node src/main.ts --name alice --create-group "Backend Team" --members 2,3

# bob joins (no flags = most recently updated group)
IM_INTERNAL_SECRET=<backend secret> node src/main.ts --name bob

# list all of bob's groups (title, id, member count, your role) and exit
IM_INTERNAL_SECRET=<backend secret> node src/main.ts groups --name bob

# list every group in the system via the admin API
IM_ADMIN_USERNAME=admin IM_ADMIN_PASSWORD=<admin password> node src/main.ts groups

# …or a specific conversation
node src/main.ts --name bob --credential im1.... --conversation 01J2Q7D4…
```

With no `--create-group`/`--conversation` and no existing groups, the client
offers to create one interactively (title + member ids) instead of failing.
Group members are numeric user ids — each user's id is shown in their chat
header and in the login system line, so share it with the group creator.

Open several terminals with different `--name`s to chat. Flags:

| Flag | Purpose |
| --- | --- |
| `--name` | Username (required); also the identity uuid unless `--uuid` is set |
| `--nickname` | Display name |
| `--credential` | Use an existing credential instead of minting one |
| `--internal-secret` | Secret for minting (env `IM_INTERNAL_SECRET`) |
| `--admin-username` / `--admin-password` | Admin console credentials for `groups` without `--name` (env `IM_ADMIN_USERNAME` / `IM_ADMIN_PASSWORD`) |
| `--backend` | Backend URL (env `IM_BACKEND_URL`, default `http://127.0.0.1:1905`) |
| `--gateway` | Gateway WS URL (env `IM_GATEWAY_URL`, default `ws://127.0.0.1:1910/ws`) |
| `--conversation` | Join an existing conversation by id |
| `--join` | Join one of your groups by exact title (most recent if duplicated) |
| `--create-group` | Create a group with this title |
| `--members` | Comma-separated numeric member ids for `--create-group` |

In-chat commands: `/add <id>[,<id>...]` (owner/admin adds members),
`/remove <id>` (owner/admin removes a member), `/members`, `/sync`, `/help`,
`/exit`.

## Files

| Path | Role |
| --- | --- |
| `src/api.ts` | Typed REST client matching `deps/im/docs/api/openapi.md` |
| `src/gateway.ts` | Gateway WebSocket client: auth, ping, reconnect, event dedupe |
| `src/chat.ts` | Terminal UI (redraw-based message pane + input line) |
| `src/main.ts` | Interactive chat entry point |
| `src/demo.ts` | Automated multi-user completeness proof |
