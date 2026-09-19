// Sequence-based synchronization engine.
//
// The backend guarantees per-conversation monotonic `sequence` values and
// at-least-once event delivery, so clients deduplicate by message_id /
// event_id, order by (conversation_id, sequence), and heal gaps by fetching
// `after_sequence` (see deps/im/docs/api/openapi.md, "Client requirements").
//
// This engine owns the per-conversation last-seen sequence. All fetches for
// one conversation run serialized on a per-conversation queue — history()
// and realtime-driven drains can never interleave, so each message is
// emitted exactly once, in order. Conversations become tracked through
// track()/history(); events for untracked conversations are left to the
// application (surfaced via the raw "event" handler).

import type { IMHttpClient } from "./http.js";
import type { StorageAdapter } from "./adapter.js";
import type { ChatMessage, GatewayEvent } from "./types.js";

const PAGE_LIMIT = 200;
/** Upper bound of pages fetched per drain cycle (10 × 200 messages). */
const MAX_SYNC_PAGES = 10;

export interface SyncHandlers {
  onMessages: (messages: ChatMessage[], source: "history" | "realtime") => void;
  onMessageUpdated: (message: ChatMessage) => void;
}

interface FetchTaskOptions {
  fromSequence?: number;
  limit?: number;
  source: "history" | "realtime";
  maxPages?: number;
}

export class SyncEngine {
  private readonly http: IMHttpClient;
  private readonly handlers: SyncHandlers;
  private readonly storage?: StorageAdapter;
  private readonly storageKey: string;

  private readonly lastSeq = new Map<string, number>();
  private readonly queues = new Map<string, Promise<unknown>>();

  constructor(options: {
    http: IMHttpClient;
    handlers: SyncHandlers;
    storage?: StorageAdapter;
    storageKey?: string;
  }) {
    this.http = options.http;
    this.handlers = options.handlers;
    this.storage = options.storage;
    this.storageKey = options.storageKey ?? "airway-im.sequences";
    this.load();
  }

  // ---- Sequence bookkeeping ----

  /** Mark a conversation as tracked, raising its last-seen sequence. */
  track(conversationId: string, sequence: number): void {
    const current = this.lastSeq.get(conversationId);
    if (current === undefined) {
      this.lastSeq.set(conversationId, Math.max(0, sequence));
      this.persist();
      return;
    }
    if (sequence > current) {
      this.lastSeq.set(conversationId, sequence);
      this.persist();
    }
  }

  isTracked(conversationId: string): boolean {
    return this.lastSeq.has(conversationId);
  }

  lastSequence(conversationId: string): number {
    return this.lastSeq.get(conversationId) ?? 0;
  }

  forget(conversationId: string): void {
    this.lastSeq.delete(conversationId);
    this.queues.delete(conversationId);
    this.persist();
  }

  private load(): void {
    if (!this.storage) return;
    const raw = this.storage.get(this.storageKey);
    if (!raw) return;
    try {
      const parsed = JSON.parse(raw) as Record<string, number>;
      for (const [id, seq] of Object.entries(parsed)) {
        if (typeof seq === "number" && seq > 0) this.lastSeq.set(id, seq);
      }
    } catch {
      // corrupted cache: start clean rather than fail
    }
  }

  private persist(): void {
    if (!this.storage) return;
    const out: Record<string, number> = {};
    for (const [id, seq] of this.lastSeq) out[id] = seq;
    this.storage.set(this.storageKey, JSON.stringify(out));
  }

  // ---- Serialized fetching ----

  /** Serialized per-conversation fetch; returns the messages it emitted. */
  async fetchFrom(
    conversationId: string,
    options: {
      fromSequence?: number;
      limit?: number;
      source?: "history" | "realtime";
      maxPages?: number;
    } = {},
  ): Promise<ChatMessage[]> {
    return this.enqueue(conversationId, () =>
      this.runFetch(conversationId, {
        fromSequence: options.fromSequence,
        limit: options.limit,
        source: options.source ?? "history",
        maxPages: options.maxPages,
      }),
    );
  }

  /** Serialized reload of one masked (moderated) message. */
  reloadMessage(conversationId: string, event: GatewayEvent): Promise<ChatMessage | undefined> {
    return this.enqueue(conversationId, async () => {
      if (event.sequence === undefined) return undefined;
      // The sequence may already be cached; reload starting at sequence - 1
      // and replace the local message with the masked API response.
      const page = await this.http.listMessages(conversationId, {
        afterSequence: event.sequence - 1,
        limit: 10,
      });
      return page.find((m) => m.id === event.message_id);
    });
  }

  /** Resynchronize every tracked conversation after a (re)connect. */
  resyncAll(): Promise<void> {
    const jobs = [...this.lastSeq.keys()].map((id) => this.fetchFrom(id, { source: "realtime" }));
    return Promise.all(jobs).then(() => undefined);
  }

  private enqueue<T>(conversationId: string, task: () => Promise<T>): Promise<T> {
    const previous = this.queues.get(conversationId) ?? Promise.resolve();
    // The next task runs whether the previous one resolved or failed, so a
    // single network error cannot wedge the conversation's queue.
    const result = previous.then(task, task);
    this.queues.set(
      conversationId,
      result.catch(() => undefined),
    );
    return result;
  }

  private async runFetch(conversationId: string, options: FetchTaskOptions): Promise<ChatMessage[]> {
    const limit = options.limit ?? PAGE_LIMIT;
    let from = options.fromSequence ?? this.lastSequence(conversationId);
    const maxPages = options.maxPages ?? MAX_SYNC_PAGES;
    const all: ChatMessage[] = [];

    for (let page = 0; page < maxPages; page += 1) {
      const messages = await this.http.listMessages(conversationId, {
        afterSequence: from,
        limit,
      });
      if (messages.length === 0) break;
      all.push(...messages);
      this.handlers.onMessages(messages, options.source);
      from = messages[messages.length - 1].sequence;
      if (messages.length < limit) break;
    }
    // Track even when the page was empty: the caller has seen everything up
    // to `from` (0 for fresh conversations), so later events can sync.
    this.track(conversationId, from);
    return all;
  }

  // ---- Event-driven sync ----

  /** Handle a gateway message.created event (serialized per conversation). */
  handleMessageEvent(event: GatewayEvent): void {
    const conversationId = event.conversation_id;
    if (!this.isTracked(conversationId)) return; // untracked: app decides
    const target = event.sequence;
    if (target !== undefined && target <= this.lastSequence(conversationId)) {
      return; // duplicate or already-applied (e.g. our own send)
    }
    void this.fetchFrom(conversationId, { source: "realtime" }).catch(() => {
      // The next event for this conversation retries; nothing is lost because
      // the sequence tracker still points at the last applied message.
    });
  }

  /** Handle a gateway message.moderated event by reloading the masked message. */
  async handleMessageModeratedEvent(event: GatewayEvent): Promise<void> {
    if (!this.isTracked(event.conversation_id)) return;
    const updated = await this.reloadMessage(event.conversation_id, event);
    if (updated) this.handlers.onMessageUpdated(updated);
  }
}
