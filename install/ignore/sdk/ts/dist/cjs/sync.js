"use strict";
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
Object.defineProperty(exports, "__esModule", { value: true });
exports.SyncEngine = void 0;
const PAGE_LIMIT = 200;
/** Upper bound of pages fetched per drain cycle (10 × 200 messages). */
const MAX_SYNC_PAGES = 10;
class SyncEngine {
    constructor(options) {
        this.lastSeq = new Map();
        this.queues = new Map();
        this.http = options.http;
        this.handlers = options.handlers;
        this.storage = options.storage;
        this.storageKey = options.storageKey ?? "airway-im.sequences";
        this.load();
    }
    // ---- Sequence bookkeeping ----
    /** Mark a conversation as tracked, raising its last-seen sequence. */
    track(conversationId, sequence) {
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
    isTracked(conversationId) {
        return this.lastSeq.has(conversationId);
    }
    lastSequence(conversationId) {
        return this.lastSeq.get(conversationId) ?? 0;
    }
    forget(conversationId) {
        this.lastSeq.delete(conversationId);
        this.queues.delete(conversationId);
        this.persist();
    }
    load() {
        if (!this.storage)
            return;
        const raw = this.storage.get(this.storageKey);
        if (!raw)
            return;
        try {
            const parsed = JSON.parse(raw);
            for (const [id, seq] of Object.entries(parsed)) {
                if (typeof seq === "number" && seq > 0)
                    this.lastSeq.set(id, seq);
            }
        }
        catch {
            // corrupted cache: start clean rather than fail
        }
    }
    persist() {
        if (!this.storage)
            return;
        const out = {};
        for (const [id, seq] of this.lastSeq)
            out[id] = seq;
        this.storage.set(this.storageKey, JSON.stringify(out));
    }
    // ---- Serialized fetching ----
    /** Serialized per-conversation fetch; returns the messages it emitted. */
    async fetchFrom(conversationId, options = {}) {
        return this.enqueue(conversationId, () => this.runFetch(conversationId, {
            fromSequence: options.fromSequence,
            limit: options.limit,
            maxPages: options.maxPages,
        }));
    }
    /** Serialized reload of one masked (moderated) message. */
    reloadMessage(conversationId, event) {
        return this.enqueue(conversationId, async () => {
            if (event.sequence === undefined)
                return undefined;
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
    resyncAll() {
        const jobs = [...this.lastSeq.keys()].map((id) => this.fetchFrom(id));
        return Promise.all(jobs).then(() => undefined);
    }
    enqueue(conversationId, task) {
        const previous = this.queues.get(conversationId) ?? Promise.resolve();
        // The next task runs whether the previous one resolved or failed, so a
        // single network error cannot wedge the conversation's queue.
        const result = previous.then(task, task);
        this.queues.set(conversationId, result.catch(() => undefined));
        return result;
    }
    async runFetch(conversationId, options) {
        const limit = options.limit ?? PAGE_LIMIT;
        let from = options.fromSequence ?? this.lastSequence(conversationId);
        const maxPages = options.maxPages ?? MAX_SYNC_PAGES;
        const all = [];
        for (let page = 0; page < maxPages; page += 1) {
            const messages = await this.http.listMessages(conversationId, {
                afterSequence: from,
                limit,
            });
            if (messages.length === 0)
                break;
            all.push(...messages);
            this.handlers.onMessages(messages);
            from = messages[messages.length - 1].sequence;
            if (messages.length < limit)
                break;
        }
        // Track even when the page was empty: the caller has seen everything up
        // to `from` (0 for fresh conversations), so later events can sync.
        this.track(conversationId, from);
        return all;
    }
    // ---- Event-driven sync ----
    /** Handle a gateway message.created event (serialized per conversation). */
    handleMessageEvent(event) {
        const conversationId = event.conversation_id;
        if (!this.isTracked(conversationId))
            return; // untracked: app decides
        const target = event.sequence;
        if (target !== undefined && target <= this.lastSequence(conversationId)) {
            return; // duplicate or already-applied (e.g. our own send)
        }
        void this.fetchFrom(conversationId).catch(() => {
            // The next event for this conversation retries; nothing is lost because
            // the sequence tracker still points at the last applied message.
        });
    }
    /** Handle a gateway message.moderated event by reloading the masked message. */
    async handleMessageModeratedEvent(event) {
        if (!this.isTracked(event.conversation_id))
            return;
        const updated = await this.reloadMessage(event.conversation_id, event);
        if (updated)
            this.handlers.onMessageUpdated(updated);
    }
}
exports.SyncEngine = SyncEngine;
