// Interactive multi-user group chat TUI over the Airway IM plugin.
//
// Usage:
//   node src/main.ts --name alice [--nickname Alice]
//     [--conversation <id> | --join "<group title>" | --create-group "Title" --members 2,3]
//     [--backend http://127.0.0.1:1905] [--gateway ws://127.0.0.1:1910/ws]
//     [--credential im1.... | --internal-secret <secret>]
//   node src/main.ts groups --name alice   # list my groups and exit
//   node src/main.ts groups                # list ALL groups (admin API; needs
//                                          #   IM_ADMIN_USERNAME / IM_ADMIN_PASSWORD)
//
// With no --conversation/--create-group, the most recently updated group is
// joined. Credentials are minted via the internal API when --internal-secret
// (or IM_INTERNAL_SECRET) is given; otherwise pass --credential.

import * as readline from "node:readline/promises";

import { AdminClient, IMClient, type ChatMessage, type Conversation, type ConversationMember } from "./api.ts";
import { GatewayClient } from "./gateway.ts";
import { ChatUI } from "./chat.ts";

interface Args {
  command?: string;
  name?: string;
  uuid?: string;
  nickname?: string;
  credential?: string;
  internalSecret?: string;
  adminUsername?: string;
  adminPassword?: string;
  backend: string;
  gateway: string;
  conversation?: string;
  join?: string;
  createGroup?: string;
  members?: string;
}

function parseArgs(argv: string[]): Args {
  const args: Args = {
    backend: process.env.IM_BACKEND_URL ?? "http://127.0.0.1:1905",
    gateway: process.env.IM_GATEWAY_URL ?? "ws://127.0.0.1:1910/ws",
    internalSecret: process.env.IM_INTERNAL_SECRET,
    adminUsername: process.env.IM_ADMIN_USERNAME,
    adminPassword: process.env.IM_ADMIN_PASSWORD,
  };
  for (let i = 0; i < argv.length; i++) {
    const flag = argv[i];
    if (!flag.startsWith("--")) {
      if (args.command !== undefined) throw new Error(`unexpected argument: ${flag}`);
      args.command = flag;
      continue;
    }
    const value = () => {
      const v = argv[++i];
      if (v === undefined) throw new Error(`missing value for ${flag}`);
      return v;
    };
    switch (flag) {
      case "--name": args.name = value(); break;
      case "--uuid": args.uuid = value(); break;
      case "--nickname": args.nickname = value(); break;
      case "--credential": args.credential = value(); break;
      case "--internal-secret": args.internalSecret = value(); break;
      case "--admin-username": args.adminUsername = value(); break;
      case "--admin-password": args.adminPassword = value(); break;
      case "--backend": args.backend = value(); break;
      case "--gateway": args.gateway = value(); break;
      case "--conversation": args.conversation = value(); break;
      case "--join": args.join = value(); break;
      case "--create-group": args.createGroup = value(); break;
      case "--members": args.members = value(); break;
      default: throw new Error(`unknown flag: ${flag}`);
    }
  }
  return args;
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  if (args.command !== undefined && args.command !== "groups") {
    console.error(`error: unknown command "${args.command}" (available: groups)`);
    process.exit(2);
  }

  // `groups` without --name lists every group in the system via the admin API.
  if (args.command === "groups" && !args.name) {
    await listAllGroups(args);
    return;
  }

  if (!args.name) {
    console.error("error: --name is required (see the header comment for usage)");
    process.exit(2);
  }

  const client = args.credential
    ? IMClient.withCredential(args.backend, args.credential)
    : await IMClient.mint(args.backend, mustSecret(args), {
        uuid: args.uuid ?? args.name,
        name: args.name,
        nickname: args.nickname,
      });

  const me = await client.me();

  if (args.command === "groups") {
    await listMyGroups(client, me.id);
    return;
  }

  // Resolve the conversation to join.
  let conversationId: string;
  let title: string;
  if (args.createGroup !== undefined) {
    const memberIds = parseMemberIds(args.members ?? "");
    const conv = await createGroupOrExit(client, args.createGroup || null, memberIds);
    conversationId = conv.id;
    title = conv.title ?? conv.id;
    console.log(`created group "${title}" (${conv.id})`);
  } else if (args.conversation) {
    conversationId = args.conversation;
    title = args.conversation;
  } else if (args.join !== undefined) {
    // Join one of my groups by exact title; listGroups is ordered by
    // updated_at desc, so the first match is the most recently active one.
    const groups = await client.listGroups();
    const matches = groups.filter((g) => g.title === args.join);
    if (matches.length === 0) {
      const available = groups.map((g) => g.title ?? g.id).join(", ");
      console.error(
        `error: no group named "${args.join}" among your groups${available ? ` (you have: ${available})` : " (you have none)"}.`,
      );
      console.error("note: --join selects a group you already belong to; only an owner/admin can add you to a new one.");
      process.exit(2);
    }
    conversationId = matches[0].id;
    title = matches[0].title ?? matches[0].id;
    if (matches.length > 1) {
      console.log(`found ${matches.length} groups named "${args.join}"; joining the most recent (${conversationId})`);
    }
  } else {
    const groups = await client.listGroups();
    if (groups.length === 0) {
      // First run: offer to create a group interactively instead of failing.
      console.log(`logged in as ${me.username} (id ${me.id}) — you have no groups yet.`);
      console.log("Group members are numeric user ids; each user's id is shown in their chat header.");
      const created = await promptCreateGroup(client);
      if (!created) {
        console.error('aborted. Use --create-group "Title" --members <id>[,<id>...] next time.');
        process.exit(2);
      }
      conversationId = created.id;
      title = created.title ?? created.id;
    } else {
      conversationId = groups[0].id;
      title = groups[0].title ?? groups[0].id;
      if (groups.length > 1) {
        console.log(`joining most recent group "${title}"; pick another with --conversation <id>`);
      }
    }
  }

  const details = await client.getConversation(conversationId);
  if (details.type === "group") {
    title = `${title} · members: ${memberNames(details.members, me.id).join(", ")}`;
  }

  // Message state: dedupe by message_id, order by sequence.
  const seenMessages = new Set<string>();
  let lastSequence = 0;
  const ui = new ChatUI({ title, selfName: me.nickname ?? me.username, selfId: me.id });

  const append = (msg: ChatMessage): void => {
    if (seenMessages.has(msg.id)) return;
    seenMessages.add(msg.id);
    lastSequence = Math.max(lastSequence, msg.sequence);
    ui.addMessage(msg, me.id);
  };

  // Serialize syncs; onEvent and reconnect can trigger them concurrently.
  let syncChain: Promise<void> = Promise.resolve();
  const sync = (): void => {
    syncChain = syncChain.then(async () => {
      const missed = await client.listMessages(conversationId, lastSequence);
      for (const msg of missed) append(msg);
    }).catch((err: unknown) => ui.addSystem(`sync failed: ${String(err)}`));
  };

  const history = await client.listMessages(conversationId, 0);

  const refreshTitle = (): void => {
    void client.getConversation(conversationId).then((d) => {
      if (d.type !== "group") return;
      const base = title.split(" · members:")[0];
      ui.setTitle(`${base} · members: ${memberNames(d.members, me.id).join(", ")}`);
    }).catch(() => {});
  };

  const gateway = new GatewayClient(args.gateway, client.credentialValue, {
    onStatus: (status) => ui.setStatus(status),
    onReady: () => sync(), // catch up after (re)connect
    onEvent: (event) => {
      if (event.conversation_id !== conversationId) {
        ui.addSystem(`new event in another conversation ${event.conversation_id}`);
        return;
      }
      if (event.event === "message.created") {
        sync(); // sequence-gap-safe: always fetch from lastSequence
      } else if (event.event === "message.moderated") {
        void client
          .listMessages(conversationId, Math.max(0, (event.sequence ?? 1) - 1), 1)
          .then((msgs) => { if (msgs[0]) ui.replaceMessage(msgs[0]); })
          .catch(() => {});
      } else if (event.event === "conversation.member_added") {
        const added = (event.added_user_ids ?? []).join(", ");
        ui.addSystem(`member(s) ${added} joined the group`);
        refreshTitle();
      } else if (event.event === "conversation.member_removed") {
        if (event.removed_user_id === me.id) {
          ui.addSystem("you were removed from this group — sending will fail");
        } else {
          ui.addSystem(`member ${event.removed_user_id} was removed`);
        }
        refreshTitle();
      }
    },
    onError: (err) => ui.addSystem(`gateway error: ${err.message}`),
  });

  ui.start((line) => {
    if (line.startsWith("/")) {
      handleCommand(line, ui, client, conversationId, gateway, sync, me.id);
      return;
    }
    void client
      .sendMessage(conversationId, line, { idempotencyKey: crypto.randomUUID() })
      .then(append)
      .catch((err: unknown) => ui.addSystem(`send failed: ${String(err)}`));
  });

  ui.addSystem(`logged in as ${me.username} (id ${me.id}, uuid ${me.uuid})`);
  for (const msg of history) append(msg);
  if (history.length > 0) ui.addSystem(`loaded ${history.length} earlier message(s)`);
  gateway.connect();
}

function mustSecret(args: Args): string {
  if (!args.internalSecret) {
    console.error("error: pass --credential, or --internal-secret / IM_INTERNAL_SECRET to mint one");
    process.exit(2);
  }
  return args.internalSecret;
}

function parseMemberIds(raw: string): number[] {
  return raw
    .split(",")
    .map((s) => Number(s.trim()))
    .filter((n) => Number.isInteger(n) && n > 0);
}

async function listMyGroups(client: IMClient, selfId: number): Promise<void> {
  const groups = await client.listGroups();
  if (groups.length === 0) {
    console.log("you have no groups yet — create one with --create-group \"Title\" --members <id>[,<id>...]");
    return;
  }
  const rows = await Promise.all(groups.map(async (g) => {
    const details = await client.getConversation(g.id);
    const me = details.members.find((m) => m.id === selfId);
    return { group: g, members: details.members.length, role: me?.role ?? "?" };
  }));
  console.log(`${rows.length} group(s):`);
  for (const { group, members, role } of rows) {
    const updated = new Date(group.updated_at).toLocaleString();
    console.log(`  ${group.title ?? "(untitled)"}  ${group.id}  ${members} members · you: ${role} · updated ${updated}`);
  }
}

async function listAllGroups(args: Args): Promise<void> {
  if (!args.adminUsername || !args.adminPassword) {
    console.error("error: listing all groups requires admin credentials — set IM_ADMIN_USERNAME / IM_ADMIN_PASSWORD (or --admin-username / --admin-password)");
    process.exit(2);
  }
  const admin = await AdminClient.login(args.backend, args.adminUsername, args.adminPassword);
  const groups = await admin.listAllGroups();
  if (groups.length === 0) {
    console.log("no groups in the system yet");
    return;
  }
  console.log(`${groups.length} group(s) in the system:`);
  for (const g of groups) {
    const latest = g.latest_message_at ? new Date(g.latest_message_at).toLocaleString() : "never";
    console.log(`  ${g.title ?? "(untitled)"}  ${g.id}  ${g.member_count} members · ${g.message_count} messages · by ${g.creator_username} · latest ${latest}`);
  }
}

function memberNames(members: ConversationMember[], selfId: number): string[] {
  return members.map((m) => `${m.nickname ?? m.username}(${m.id === selfId ? "you" : m.id})`);
}

async function createGroupOrExit(
  client: IMClient,
  title: string | null,
  memberIds: number[],
): Promise<Conversation> {
  if (memberIds.length === 0) {
    console.error("error: a group needs at least one other member id (--members <id>[,<id>...])");
    process.exit(2);
  }
  try {
    return await client.createGroup(title, memberIds);
  } catch (err) {
    console.error("error: could not create group:", err instanceof Error ? err.message : err);
    process.exit(1);
  }
}

async function promptCreateGroup(client: IMClient): Promise<Conversation | null> {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
    const title = (await rl.question('Create a group now? Title (empty to quit): ')).trim();
    if (!title) return null;
    const raw = await rl.question("Member ids, comma-separated: ");
    const memberIds = parseMemberIds(raw);
    if (memberIds.length === 0) {
      console.error("no valid member ids given");
      return null;
    }
    try {
      const conv = await client.createGroup(title, memberIds);
      console.log(`created group "${conv.title ?? conv.id}" (${conv.id})`);
      return conv;
    } catch (err) {
      console.error("could not create group:", err instanceof Error ? err.message : err);
      return null;
    }
  } finally {
    rl.close();
  }
}

function handleCommand(
  line: string,
  ui: ChatUI,
  client: IMClient,
  conversationId: string,
  gateway: GatewayClient,
  sync: () => void,
  selfId: number,
): void {
  const [cmd, ...rest] = line.split(/\s+/);
  switch (cmd) {
    case "/help":
      ui.addSystem("commands: /add <id>[,<id>...] /remove <id> /members /sync /exit — anything else is sent as a message");
      break;
    case "/add": {
      const memberIds = parseMemberIds(rest.join(","));
      if (memberIds.length === 0) {
        ui.addSystem("usage: /add <id>[,<id>...]");
        return;
      }
      void client.addMembers(conversationId, memberIds).then((d) => {
        ui.addSystem(`members: ${memberNames(d.members, selfId).join(", ")}`);
      }).catch((err: unknown) => ui.addSystem(`add failed: ${String(err)}`));
      break;
    }
    case "/remove": {
      const targetId = Number(rest[0]);
      if (!Number.isInteger(targetId) || targetId < 1) {
        ui.addSystem("usage: /remove <id>");
        return;
      }
      void client.removeMember(conversationId, targetId).then((d) => {
        ui.addSystem(`members: ${memberNames(d.members, selfId).join(", ")}`);
      }).catch((err: unknown) => ui.addSystem(`remove failed: ${String(err)}`));
      break;
    }
    case "/members":
      void client.getConversation(conversationId).then((d) => {
        ui.addSystem(
          d.members
            .map((m) => `${m.nickname ?? m.username}(${m.id === selfId ? `you, ${m.role}` : `${m.id}, ${m.role}`})`)
            .join(", "),
        );
      });
      break;
    case "/sync":
      sync();
      ui.addSystem("resync requested");
      break;
    case "/exit":
      gateway.close();
      ui.stop();
      process.exit(0);
      break;
    default:
      ui.addSystem(`unknown command ${cmd} — try /help`);
  }
}

main().catch((err: unknown) => {
  console.error("fatal:", err instanceof Error ? err.message : err);
  process.exit(1);
});
