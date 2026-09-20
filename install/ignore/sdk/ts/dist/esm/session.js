// AirwayIM: the high-level entry point combining the REST client, the
// WebSocket gateway, and the sequence-based sync engine behind one typed
// event-emitting facade. This is what mini-program apps should use.
import { IMHttpClient } from "./http.js";
import { GatewaySocket } from "./gateway.js";
import { SyncEngine } from "./sync.js";
import { DirectConversation, GroupConversation } from "./conversation.js";
import { IMError } from "./error.js";
export class AirwayIM {
    rememberKind(conversationId, kind) {
        this.conversationKinds.set(conversationId, kind);
    }
    conversationFor(id, kind, title = null) {
        const existing = this.conversations.get(id);
        if (existing)
            return existing;
        const conversation = kind === "direct" ? new DirectConversation(this, id) : new GroupConversation(this, id, title);
        this.conversations.set(id, conversation);
        return conversation;
    }
    constructor(options) {
        this.gateway = null;
        this.listeners = new Map();
        this.status = "closed";
        this.wantConnected = false;
        /** conversation id → kind, remembered from every call/event that reveals it. */
        this.conversationKinds = new Map();
        /** conversation id → conversation object; the same object (with its listeners) is
         * returned by every open/create call for that conversation. */
        this.conversations = new Map();
        if (!options.adapter) {
            // The type makes this unreachable from TypeScript; the guard is for
            // plain-JS callers, where a missing adapter would otherwise surface as
            // a confusing wx/browser error deep inside the first request.
            throw new Error("createClient requires an explicit adapter: wechatAdapter() for WeChat Mini " +
                "Programs, browserAdapter() for browsers and Node, or a custom IMAdapter.");
        }
        this.adapter = options.adapter;
        this.wsUrl = options.wsUrl;
        this.rest = new IMHttpClient({
            apiUrl: options.apiUrl,
            credential: options.credential,
            adapter: this.adapter,
            timeoutMs: options.timeoutMs,
            getCredential: options.getCredential
                ? async () => {
                    const fresh = await options.getCredential();
                    this.gateway?.setCredential(fresh);
                    return fresh;
                }
                : undefined,
        });
        this.sync = new SyncEngine({
            http: this.rest,
            storage: options.persistSequences === false ? undefined : this.adapter.storage,
            handlers: {
                onMessages: (messages, source) => {
                    for (const message of messages) {
                        this.emit("message", message, source);
                        this.conversations.get(message.conversation_id)?.emitLocal("message", message, source);
                    }
                },
                onMessageUpdated: (message) => {
                    this.emit("message.updated", message);
                    this.conversations.get(message.conversation_id)?.emitLocal("message.updated", message);
                },
            },
        });
        if (this.wsUrl) {
            this.gateway = new GatewaySocket({
                wsUrl: this.wsUrl,
                credential: options.credential,
                adapter: this.adapter,
                pingIntervalMs: options.pingIntervalMs,
                getCredential: options.getCredential
                    ? async () => {
                        const fresh = await options.getCredential();
                        this.rest.setCredential(fresh);
                        return fresh;
                    }
                    : undefined,
                callbacks: {
                    onEvent: (event) => this.handleEvent(event),
                    onStatus: (status) => {
                        this.status = status;
                        this.emit("status", status);
                    },
                    onReady: () => {
                        void this.sync.resyncAll().catch((err) => this.emit("error", err));
                    },
                    onError: (error) => this.emit("error", error),
                },
            });
        }
        if (options.autoConnect)
            this.connect();
        // Mini programs kill sockets in the background: reconnect on foreground
        // if we were connected (adapter must support onShow, WeChat does).
        if (this.adapter.onShow) {
            this.adapter.onShow(() => {
                if (this.wantConnected && this.gateway)
                    this.gateway.connect();
            });
        }
    }
    // ---- Events ----
    on(event, listener) {
        let set = this.listeners.get(event);
        if (!set) {
            set = new Set();
            this.listeners.set(event, set);
        }
        set.add(listener);
        return this;
    }
    off(event, listener) {
        this.listeners.get(event)?.delete(listener);
        return this;
    }
    emit(event, ...args) {
        const set = this.listeners.get(event);
        if (!set)
            return;
        for (const listener of [...set]) {
            try {
                listener(...args);
            }
            catch (err) {
                // A throwing listener must not break the sync loop.
                this.emit("error", err);
            }
        }
    }
    handleEvent(event) {
        this.emit("event", event);
        switch (event.event) {
            case "message.created":
                this.sync.handleMessageEvent(event);
                break;
            case "message.moderated":
                void this.sync
                    .handleMessageModeratedEvent(event)
                    .catch((err) => this.emit("error", err));
                break;
            case "conversation.member_added":
                // Member management is group-only server-side, so these events mark
                // the conversation as a group in the kind registry.
                this.rememberKind(event.conversation_id, "group");
                if (event.added_user_uuids?.length) {
                    const info = {
                        conversationId: event.conversation_id,
                        addedUserUUIDs: event.added_user_uuids,
                        event,
                    };
                    this.emit("members.added", info);
                    this.conversations.get(event.conversation_id)?.emitLocal("members.added", info);
                }
                break;
            case "conversation.member_removed":
                this.rememberKind(event.conversation_id, "group");
                if (event.removed_user_uuid !== undefined) {
                    const info = {
                        conversationId: event.conversation_id,
                        removedUserUUID: event.removed_user_uuid,
                        event,
                    };
                    this.emit("members.removed", info);
                    this.conversations.get(event.conversation_id)?.emitLocal("members.removed", info);
                }
                break;
            default:
                break;
        }
    }
    // ---- Realtime ----
    /** Open the gateway connection (no-op in REST-only mode). */
    connect() {
        if (!this.gateway) {
            this.emit("error", new IMError(-1, "No wsUrl configured; realtime is disabled", 0));
            return;
        }
        this.wantConnected = true;
        this.gateway.connect();
    }
    /** Close the gateway connection and stop reconnecting. */
    disconnect() {
        this.wantConnected = false;
        this.gateway?.close();
        if (this.gateway) {
            // close() already emits "closed"; keep our cached status in sync.
            this.status = "closed";
        }
    }
    get connectionStatus() {
        return this.status;
    }
    get isOnline() {
        return this.gateway?.isOnline ?? false;
    }
    /** Replace the credential everywhere (e.g. after your own re-login flow). */
    setCredential(credential) {
        this.rest.setCredential(credential);
        this.gateway?.setCredential(credential);
    }
    // ---- REST passthrough ----
    me() {
        return this.rest.me();
    }
    listGroups() {
        return this.rest.listGroups().then((conversations) => {
            for (const conversation of conversations)
                this.rememberKind(conversation.id, "group");
            return conversations;
        });
    }
    /** Get-or-create the direct (1:1) conversation with one user, by uuid. */
    createDirect(otherUserUUID) {
        return this.rest.createDirect(otherUserUUID).then((conversation) => {
            this.rememberKind(conversation.id, "direct");
            return this.conversationFor(conversation.id, "direct");
        });
    }
    /**
     * The existing direct conversation with one user, by uuid, as a
     * DirectConversation —
     * or null when none exists yet (createDirect get-or-creates instead).
     */
    getDirect(otherUserUUID) {
        return this.rest.getDirect(otherUserUUID).then((conversation) => {
            if (!conversation)
                return null;
            this.rememberKind(conversation.id, "direct");
            return this.conversationFor(conversation.id, "direct");
        });
    }
    /** Create a group conversation; the authenticated user becomes its owner. */
    createGroup(title, memberUUIDs) {
        return this.rest.createGroup(title, memberUUIDs).then((conversation) => {
            this.rememberKind(conversation.id, "group");
            return this.conversationFor(conversation.id, "group", conversation.title);
        });
    }
    /**
     * Open any conversation by id as a conversation object (e.g. one learned
     * from a "message" event or listGroups). The kind is answered from the
     * registry when this instance already saw the conversation, otherwise
     * fetched via REST once; rejects if the user cannot see the conversation.
     */
    async openConversation(conversationId) {
        const existing = this.conversations.get(conversationId);
        if (existing)
            return existing;
        let kind = this.conversationKinds.get(conversationId);
        if (!kind) {
            const details = await this.rest.getConversation(conversationId);
            kind = details.type;
            this.rememberKind(details.conversation_uuid, kind);
        }
        return this.conversationFor(conversationId, kind);
    }
    addMembers(conversationId, memberUUIDs) {
        // Member management is group-only server-side.
        this.rememberKind(conversationId, "group");
        return this.rest.addMembers(conversationId, memberUUIDs);
    }
    removeMembers(conversationId, userUUIDs) {
        this.rememberKind(conversationId, "group");
        return this.rest.removeMembers(conversationId, userUUIDs);
    }
    /**
     * Initial load for a conversation: fetch messages after fromSequence
     * (default: last persisted sequence, else 0), track the sequence, and emit
     * each message via the "message" event with source "history". After this,
     * the conversation is tracked and realtime events auto-heal gaps for it.
     */
    history(conversationId, options = {}) {
        return this.sync.fetchFrom(conversationId, {
            fromSequence: options.fromSequence,
            limit: options.limit,
            source: "history",
        });
    }
    /**
     * Raw ordered message page after a sequence (no state changes, no event
     * emission). Most apps should use history() + realtime events instead.
     */
    listMessages(conversationId, options = {}) {
        return this.rest.listMessages(conversationId, options);
    }
    /**
     * Send a message to a group conversation by id. Resolves with the stored
     * message; the local sequence tracker is updated so the sender's own
     * message.created event does not trigger a redundant fetch. The message is
     * also emitted via "message" only when it arrives back through
     * realtime/sync (at-least-once) — handle the return value for immediate UI
     * feedback.
     */
    async sendGroupMessage(conversationId, content, options = {}) {
        this.rememberKind(conversationId, "group");
        const message = await this.rest.sendMessage(conversationId, content, options);
        this.sync.track(conversationId, message.sequence);
        return message;
    }
    /**
     * Send a direct message to one other user, identified by their uuid:
     * get-or-create the direct conversation, then send. Same idempotency
     * semantics as sendGroupMessage.
     */
    async sendDirectMessage(otherUserUUID, content, options = {}) {
        const conversation = await this.createDirect(otherUserUUID);
        const message = await this.rest.sendMessage(conversation.id, content, options);
        this.sync.track(conversation.id, message.sequence);
        return message;
    }
    lastSequence(conversationId) {
        return this.sync.lastSequence(conversationId);
    }
    /** Drop all sync state for a conversation (e.g. after being kicked). */
    forgetConversation(conversationId) {
        this.sync.forget(conversationId);
        this.conversationKinds.delete(conversationId);
    }
    /** @internal Conversation objects: start tracking on first message listener. */
    ensureTracked(conversationId) {
        if (this.sync.isTracked(conversationId))
            return;
        void this.history(conversationId).catch((err) => this.emit("error", err));
    }
    /** @internal Conversation objects: surface listener exceptions. */
    reportError(err) {
        this.emit("error", err);
    }
    // ---- Storage ----
    /** Upload a WeChat local path or browser File/Blob; returns {key,url,size}. */
    uploadFile(filePath, dir) {
        return this.rest.uploadFile(filePath, dir);
    }
    /** Public URL for a storage key (for <image src>, wx.downloadFile, ...). */
    storageUrl(key) {
        return this.rest.storageUrl(key);
    }
}
