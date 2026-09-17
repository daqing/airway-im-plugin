// Automated end-to-end proof of the Airway IM chat backend.
//
// Spins up three users (alice, bob, carol) in one process, each with its own
// minted credential, WebSocket gateway connection and HTTP client, and runs:
//
//   1. group creation + membership listing
//   2. realtime fan-out: everyone receives everyone's messages
//   3. idempotent resend: same Idempotency-Key replays the same message
//   4. offline catch-up: a disconnected client resyncs via after_sequence
//   5. history consistency: all users read identical, gapless sequences
//
// Usage: IM_INTERNAL_SECRET=... node src/demo.ts
// Env: IM_BACKEND_URL (default http://127.0.0.1:1905),
//      IM_GATEWAY_URL (default ws://127.0.0.1:1910/ws)

import { IMClient, IMError, type ChatMessage } from "./api.ts";
import { GatewayClient, type GatewayEvent } from "./gateway.ts";

const BACKEND = process.env.IM_BACKEND_URL ?? "http://127.0.0.1:1905";
const GATEWAY = process.env.IM_GATEWAY_URL ?? "ws://127.0.0.1:1910/ws";
const INTERNAL_SECRET = process.env.IM_INTERNAL_SECRET;

if (!INTERNAL_SECRET) {
  console.error("error: IM_INTERNAL_SECRET is required to mint demo credentials");
  process.exit(2);
}

const results: { name: string; ok: boolean; detail: string }[] = [];
function report(name: string, ok: boolean, detail = ""): void {
  results.push({ name, ok, detail });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? ` — ${detail}` : ""}`);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function waitUntil(cond: () => boolean, timeoutMs: number, what: string): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (cond()) return;
    await sleep(50);
  }
  throw new Error(`timeout waiting for: ${what}`);
}

class Participant {
  client!: IMClient;
  gateway!: GatewayClient;
  id = 0;
  events: GatewayEvent[] = [];
  online = false;

  readonly name: string;

  constructor(name: string) {
    this.name = name;
  }

  async setup(): Promise<void> {
    this.client = await IMClient.mint(BACKEND, INTERNAL_SECRET!, {
      uuid: `demo-${this.name}`,
      name: this.name,
      nickname: this.name,
    });
    this.id = (await this.client.me()).id;
  }

  connect(): void {
    this.gateway = new GatewayClient(GATEWAY, this.client.credentialValue, {
      onEvent: (e) => this.events.push(e),
      onStatus: (s) => { this.online = s === "online"; },
    });
    this.gateway.connect();
  }

  disconnect(): void {
    this.gateway.close();
    this.online = false;
  }
}

async function main(): Promise<void> {
  const [alice, bob, carol, dave] = [
    new Participant("alice"),
    new Participant("bob"),
    new Participant("carol"),
    new Participant("dave"),
  ];
  for (const p of [alice, bob, carol, dave]) await p.setup();
  report("identity: mint credentials + auto-register users", true,
    `ids: alice=${alice.id} bob=${bob.id} carol=${carol.id}`);

  // 1. Group creation and membership.
  const group = await alice.client.createGroup(`demo-${Date.now()}`, [bob.id, carol.id]);
  const details = await bob.client.getConversation(group.id);
  const rolesOk =
    details.members.length === 3 &&
    details.members.find((m) => m.id === alice.id)?.role === "owner" &&
    details.members.filter((m) => m.role === "member").length === 2;
  report("conversation: create group, roles assigned", rolesOk,
    details.members.map((m) => `${m.username}:${m.role}`).join(", "));
  const bobGroups = await bob.client.listGroups();
  report("conversation: group listed for members", bobGroups.some((g) => g.id === group.id));

  // 2. Realtime fan-out.
  for (const p of [alice, bob, carol, dave]) p.connect();
  await waitUntil(() => [alice, bob, carol, dave].every((p) => p.online), 10_000, "all online");
  report("gateway: four connections authenticated", true);

  const sent: ChatMessage[] = [];
  for (const p of [alice, bob, carol]) {
    for (let i = 1; i <= 2; i++) {
      sent.push(await p.client.sendMessage(group.id, `hello from ${p.name} #${i}`, {
        idempotencyKey: crypto.randomUUID(),
      }));
    }
  }
  await waitUntil(
    () => [alice, bob, carol].every((p) =>
      p.events.filter((e) => e.event === "message.created").length >= 6),
    10_000,
    "fan-out of 6 messages to 3 clients",
  );
  const fanoutOk = [alice, bob, carol].every((p) => {
    const ids = new Set(
      p.events.filter((e) => e.event === "message.created").map((e) => e.message_id),
    );
    return sent.every((m) => ids.has(m.id));
  });
  report("realtime: every member received all 6 messages", fanoutOk);

  // 2b. Add a member to an existing group.
  const beforeAdd = [alice, bob, carol, dave].map(
    (p) => p.events.filter((e) => e.event === "conversation.member_added").length,
  );
  const addResult = await alice.client.addMembers(group.id, [dave.id]);
  const addOk =
    addResult.members.length === 4 &&
    addResult.members.some((m) => m.id === dave.id && m.role === "member");
  await waitUntil(
    () => [alice, bob, carol, dave].every(
      (p, i) => p.events.filter((e) => e.event === "conversation.member_added").length > beforeAdd[i]),
    10_000,
    "member_added fan-out",
  );
  const daveSeesGroup = (await dave.client.listGroups()).some((g) => g.id === group.id);
  const daveMsg = await dave.client.sendMessage(group.id, "dave joins in");
  await waitUntil(
    () => carol.events.some((e) => e.message_id === daveMsg.id),
    10_000,
    "message from new member",
  );
  report("membership: owner adds member, event fans out, new member chats", addOk && daveSeesGroup,
    `members=${addResult.members.length} dave_sees_group=${daveSeesGroup}`);

  // 3. Idempotent resend.
  const idemKey = crypto.randomUUID();
  const first = await bob.client.sendMessage(group.id, "retry me", { idempotencyKey: idemKey });
  const replay = await bob.client.sendMessage(group.id, "retry me", { idempotencyKey: idemKey });
  const replayed = first.id === replay.id && first.sequence === replay.sequence;
  await waitUntil(
    () => alice.events.filter((e) => e.message_id === first.id).length >= 1,
    10_000,
    "idempotent message delivery",
  );
  await sleep(1500); // allow any erroneous duplicate delivery to arrive
  const dupes = alice.events.filter((e) => e.message_id === first.id).length;
  report("messages: Idempotency-Key replays same message, delivered once",
    replayed && dupes === 1, `replayed=${replayed} deliveries=${dupes}`);

  // 4. Offline catch-up.
  const beforeOffline = carol.events.filter((e) => e.event === "message.created").length;
  carol.disconnect();
  await sleep(300);
  const missed: ChatMessage[] = [];
  for (let i = 1; i <= 3; i++) {
    missed.push(await alice.client.sendMessage(group.id, `while you were away ${i}`));
  }
  await sleep(1500); // delivery pushes, but carol is offline
  const offlineCount = carol.events.filter((e) => e.event === "message.created").length;
  carol.connect();
  await waitUntil(() => carol.online, 10_000, "carol reconnect");
  const catchup = await carol.client.listMessages(
    group.id,
    missed[0].sequence - 1,
  );
  const catchupOk =
    offlineCount === beforeOffline &&
    missed.every((m) => catchup.some((c) => c.id === m.id && c.content === m.content));
  report("sync: offline client missed pushes, caught up via after_sequence", catchupOk,
    `events while offline=${offlineCount - beforeOffline}, recovered=${catchup.length}`);

  // 5. History consistency across all members.
  const histories = await Promise.all(
    [alice, bob, carol, dave].map((p) => p.client.listMessages(group.id, 0)),
  );
  const reference = histories[0];
  const sequences = reference.map((m) => m.sequence);
  const gapless = sequences.every((s, i) => s === i + 1);
  const identical = histories.every(
    (h) => h.length === reference.length &&
      h.every((m, i) => m.id === reference[i].id && m.content === reference[i].content),
  );
  report("history: identical, gapless sequence across all members",
    identical && gapless, `${reference.length} messages, sequences 1..${sequences.at(-1)}`);

  // 6. Remove a member.
  const beforeRemove = [alice, bob, carol, dave].map(
    (p) => p.events.filter((e) => e.event === "conversation.member_removed").length,
  );
  const removeResult = await alice.client.removeMember(group.id, dave.id);
  const removedFromResponse =
    removeResult.members.length === 3 && !removeResult.members.some((m) => m.id === dave.id);
  await waitUntil(
    () => [alice, bob, carol, dave].every(
      (p, i) => p.events.filter((e) => e.event === "conversation.member_removed").length > beforeRemove[i]),
    10_000,
    "member_removed fan-out (including the removed user)",
  );
  const daveStillSees = (await dave.client.listGroups()).some((g) => g.id === group.id);
  let daveSendDenied = false;
  try {
    await dave.client.sendMessage(group.id, "am I still here?");
  } catch (err) {
    daveSendDenied = err instanceof IMError && err.status === 403;
  }
  // Removing again is an idempotent no-op.
  const again = await alice.client.removeMember(group.id, dave.id);
  report("membership: owner removes member, kick event received, access revoked",
    removedFromResponse && !daveStillSees && daveSendDenied && again.members.length === 3,
    `in_list=${daveStillSees} send_denied=${daveSendDenied}`);

  for (const p of [alice, bob, carol, dave]) p.disconnect();

  const failed = results.filter((r) => !r.ok);
  console.log(`\n${results.length - failed.length}/${results.length} checks passed`);
  if (failed.length > 0) process.exit(1);
}

main().catch((err: unknown) => {
  console.error("demo aborted:", err instanceof Error ? err.message : err);
  process.exit(1);
});
