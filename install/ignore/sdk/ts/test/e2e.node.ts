// End-to-end verification of the SDK against a running backend stack
// (backend :1905, gateway :1910, delivery :1920 — see the repo root README).
// Mirrors client/src/demo.ts: mint dev credentials, then exercise identity,
// direct + group conversations, realtime fan-out, reconnect resync,
// idempotent retry, moderation masking, and storage upload.
// Phase 2 re-runs the core chat flow through the built-in browserAdapter.

import { createIM, IMError, browserAdapter } from "../dist/esm/index.js";
import type { AirwayIM } from "../dist/esm/index.js";
import type { ChatMessage } from "../dist/esm/index.js";
import { nodeAdapter } from "./node-adapter.ts";

// Node lacks localStorage (the browser adapter reads it lazily); shim it so
// the adapter's sequence persistence path is exercised here too.
if (typeof (globalThis as { localStorage?: unknown }).localStorage === "undefined") {
  const store = new Map<string, string>();
  (globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
  };
}

const API_URL = process.env.IM_API_URL ?? "http://127.0.0.1:1905";
const WS_URL = process.env.IM_WS_URL ?? "ws://127.0.0.1:1910";
const INTERNAL_URL = process.env.IM_INTERNAL_URL ?? "http://127.0.0.1:1906";
const INTERNAL_SECRET = process.env.IM_INTERNAL_SECRET ?? "dev-only-internal-secret";
const ADMIN_USER = process.env.IM_ADMIN_USERNAME ?? "admin";
const ADMIN_PASS = process.env.IM_ADMIN_PASSWORD ?? "dev-only-admin";

let passed = 0;
function ok(label: string, condition: boolean, detail = ""): void {
  if (!condition) {
    console.error(`✗ ${label}${detail ? ` — ${detail}` : ""}`);
    process.exit(1);
  }
  passed += 1;
  console.log(`✓ ${label}`);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitFor<T>(
  label: string,
  poll: () => T | undefined | null | false,
  timeoutMs = 15_000,
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = poll();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timeout waiting for: ${label}`);
    await sleep(100);
  }
}

async function mint(name: string): Promise<string> {
  const res = await fetch(`${INTERNAL_URL}/internal/v1/credentials`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-IM-Internal-Secret": INTERNAL_SECRET,
    },
    body: JSON.stringify({ uuid: `${name}-${Math.random().toString(36).slice(2, 10)}`, name }),
  });
  const body = (await res.json()) as { code: number; data?: { credential: string } };
  if (body.code !== 0 || !body.data) throw new Error(`mint failed for ${name}: ${res.status}`);
  return body.data.credential;
}

interface Collector {
  messages: ChatMessage[];
  updates: ChatMessage[];
  added: number;
  removed: number;
  errors: Error[];
}

function collect(im: AirwayIM): Collector {
  const c: Collector = { messages: [], updates: [], added: 0, removed: 0, errors: [] };
  im.on("message", (m) => c.messages.push(m));
  im.on("message.updated", (m) => c.updates.push(m));
  im.on("members.added", () => (c.added += 1));
  im.on("members.removed", () => (c.removed += 1));
  im.on("error", (e) => c.errors.push(e));
  return c;
}

function waitOnline(im: AirwayIM): Promise<void> {
  return new Promise((resolve, reject) => {
    if (im.isOnline) return resolve();
    im.on("status", (s) => {
      if (s === "online") resolve();
    });
    im.on("error", (e) => reject(e));
  });
}

async function main(): Promise<void> {
  console.log(`e2e against ${API_URL} (ws ${WS_URL})`);

  // ---- Identity ----
  const [aliceCred, bobCred, carolCred] = await Promise.all([
    mint("sdk-alice"),
    mint("sdk-bob"),
    mint("sdk-carol"),
  ]);
  ok("mint three dev credentials", Boolean(aliceCred && bobCred && carolCred));

  const alice = createIM({
    apiUrl: API_URL,
    wsUrl: WS_URL,
    credential: aliceCred,
    adapter: nodeAdapter(),
    persistSequences: false,
  });
  const bob = createIM({
    apiUrl: API_URL,
    wsUrl: WS_URL,
    credential: bobCred,
    adapter: nodeAdapter(),
    persistSequences: false,
  });
  const carol = createIM({ apiUrl: API_URL, credential: carolCred, adapter: nodeAdapter() });

  const aliceMe = await alice.me();
  const bobMe = await bob.me();
  const carolMe = await carol.me();
  ok("me(): profiles resolve", Boolean(aliceMe.uuid) && Boolean(bobMe.uuid) && Boolean(carolMe.uuid));

  // ---- Realtime connect (auth first frame) ----
  const aliceEvents = collect(alice);
  const bobEvents = collect(bob);
  alice.connect();
  bob.connect();
  await waitOnline(alice);
  await waitOnline(bob);
  ok("gateway online for both users", alice.isOnline && bob.isOnline);

  // ---- Direct conversation + realtime fan-out ----
  const direct = await alice.createDirect(bobMe.uuid);
  ok("direct get-or-create", direct.kind === "direct" && direct.id.length === 26);
  await bob.history(direct.id); // track the conversation before traffic flows

  const sent = await alice.sendMessage(direct.id, "hello **bob**", {
    contentType: "text/markdown",
  });
  ok("sendMessage returns stored message", sent.sequence === 1 && sent.sender.uuid === aliceMe.uuid);

  const received = await waitFor("bob receives direct message in order", () =>
    bobEvents.messages.find((m) => m.id === sent.id),
  );
  ok("realtime fan-out to recipient", received.content === "hello **bob**");
  ok(
    "no duplicate emission for sender (own sequence tracked)",
    !aliceEvents.messages.some((m) => m.id === sent.id),
  );

  // ---- Group conversation ----
  const group = await alice.createGroup("SDK E2E Group", [bobMe.uuid]);
  ok("group created", group.kind === "group");

  const bobGroups = await bob.listGroups();
  ok("listGroups sees the new group", bobGroups.some((g) => g.id === group.id));

  await bob.history(group.id);

  const groupMsg = await alice.sendMessage(group.id, "first group message", {
    idempotencyKey: "e2e-fixed-key",
    retries: 0,
  });
  await waitFor("bob receives group message", () =>
    bobEvents.messages.find((m) => m.id === groupMsg.id),
  );

  // ---- Idempotent replay ----
  const replay = await alice.sendMessage(group.id, "first group message", {
    idempotencyKey: "e2e-fixed-key",
    retries: 0,
  });
  ok("same idempotency key replays the original message", replay.id === groupMsg.id);
  try {
    await alice.sendMessage(group.id, "DIFFERENT BODY", { idempotencyKey: "e2e-fixed-key", retries: 0 });
    ok("key reuse with different body rejected", false);
  } catch (err) {
    const e = err as IMError;
    ok("key reuse with different body rejected", e.code === 11002, `code=${e.code}`);
  }

  // ---- Member management + events ----
  const details = await alice.addMembers(group.id, [carolMe.uuid]);
  ok("addMembers returns details with carol", details.members.some((m) => m.uuid === carolMe.uuid));
  await waitFor("bob sees members.added event", () => bobEvents.added > 0);

  const removedDetails = await alice.removeMember(group.id, carolMe.uuid);
  ok("removeMember drops carol", !removedDetails.members.some((m) => m.uuid === carolMe.uuid));
  await waitFor("bob sees members.removed event", () => bobEvents.removed > 0);

  // Carol (kicked) must be denied group access now.
  try {
    await carol.listMessages(group.id, { afterSequence: 0 });
    ok("kicked member cannot read group messages", false);
  } catch (err) {
    const e = err as IMError;
    ok("kicked member cannot read group messages", e.status === 403 || e.status === 404, `status=${e.status}`);
  }

  // ---- Offline recovery: disconnect, send, reconnect, resync ----
  bob.disconnect();
  await sleep(200);
  const missed = await alice.sendMessage(group.id, "sent while bob offline", { retries: 0 });
  await sleep(500); // let delivery push to (absent) bob only
  ok("message sent while bob offline", Boolean(missed.id));
  bob.connect();
  await waitOnline(bob);
  const recovered = await waitFor("bob recovers offline message after reconnect", () =>
    bobEvents.messages.find((m) => m.id === missed.id),
    15_000,
  );
  ok("recovered message content matches", recovered.content === "sent while bob offline");

  // ---- Moderation: admin marks a message illegal → message.updated "***" ----
  const loginRes = await fetch(`${API_URL}/admin/api/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: ADMIN_USER, password: ADMIN_PASS }),
  });
  const login = (await loginRes.json()) as { code: number; data?: { token: string } };
  ok("admin login (for moderation step)", login.code === 0 && Boolean(login.data?.token));
  if (login.data?.token) {
    const modRes = await fetch(`${API_URL}/admin/api/messages/${groupMsg.id}/mark-illegal`, {
      method: "POST",
      headers: { Authorization: `Bearer ${login.data.token}` },
    });
    ok("mark-illegal accepted", modRes.status === 200);
    const masked = await waitFor("alice receives message.updated with mask", () =>
      aliceEvents.updates.find((m) => m.id === groupMsg.id && m.content === "***"),
    );
    ok("moderated content is masked", masked.content === "***");
  }

  // ---- Storage upload (development-stage public API) ----
  const uploaded = await alice.uploadFile("ignored-local-path", "sdk-e2e");
  ok("uploadFile returns key/url/size", Boolean(uploaded.key) && uploaded.size > 0);
  ok(
    "storageUrl is absolute and keeps the key",
    alice.storageUrl(uploaded.key).startsWith(API_URL) && alice.storageUrl(uploaded.key).includes("storage/"),
  );

  // ---- REST-only instance works without wsUrl ----
  const restOnly = await carol.me();
  ok("REST-only instance (no wsUrl)", restOnly.uuid === carolMe.uuid);

  alice.disconnect();
  bob.disconnect();

  await browserAdapterPhase();

  console.log(`\nall ${passed} checks passed`);
}

/** Phase 2: the same chat flow through the built-in browser adapter. */
async function browserAdapterPhase(): Promise<void> {
  const [credA, credB] = await Promise.all([mint("sdk-web-alice"), mint("sdk-web-bob")]);
  const alice = createIM({
    apiUrl: API_URL,
    wsUrl: WS_URL,
    credential: credA,
    adapter: browserAdapter(),
    persistSequences: true,
  });
  const bob = createIM({
    apiUrl: API_URL,
    wsUrl: WS_URL,
    credential: credB,
    adapter: browserAdapter(),
    persistSequences: true,
  });
  const bobMe = await bob.me();
  ok("browserAdapter: me()", Boolean(bobMe.uuid));

  const bobEvents = collect(bob);
  alice.connect();
  bob.connect();
  await waitOnline(alice);
  await waitOnline(bob);
  ok("browserAdapter: gateway online", alice.isOnline && bob.isOnline);

  const direct = await alice.createDirect(bobMe.uuid);
  await bob.history(direct.id);
  const sent = await alice.sendMessage(direct.id, "hello from the browser adapter");
  const received = await waitFor("browserAdapter: realtime receive", () =>
    bobEvents.messages.find((m) => m.id === sent.id),
  );
  ok("browserAdapter: realtime receive", received.content === "hello from the browser adapter");

  // Upload a File/Blob directly (no string path needed in the browser).
  const uploaded = await alice.uploadFile(new Blob(["browser adapter e2e upload"]), "sdk-e2e-browser");
  ok("browserAdapter: uploadFile(File/Blob)", Boolean(uploaded.key) && uploaded.size > 0);

  // Reconnect resync + sequence persistence through localStorage.
  bob.disconnect();
  await sleep(200);
  const missed = await alice.sendMessage(direct.id, "offline while browser bob is away");
  bob.connect();
  await waitOnline(bob);
  await waitFor("browserAdapter: resync after reconnect", () =>
    bobEvents.messages.find((m) => m.id === missed.id),
  );
  ok("browserAdapter: recovered offline message", true);
  const stored = (globalThis as { localStorage: { getItem(key: string): string | null } })
    .localStorage.getItem("airway-im.sequences");
  ok("browserAdapter: sequences persisted to localStorage", Boolean(stored && stored.includes(direct.id)));

  alice.disconnect();
  bob.disconnect();
}

main().catch((err) => {
  console.error("e2e failed:", err);
  process.exit(1);
});
