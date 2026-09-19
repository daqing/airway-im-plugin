// AirwayIM: the high-level entry point combining the REST client, the
// WebSocket gateway, and the sequence-based sync engine behind one typed
// event-emitting facade. This is what mini-program apps should use.
import { IMHttpClient } from "./http.js";
import { GatewaySocket } from "./gateway.js";
import { SyncEngine } from "./sync.js";
import { IMError } from "./error.js";
import { wechatAdapter } from "./wechat.js";
export class AirwayIM {
    constructor(options) {
        this.gateway = null;
        this.listeners = new Map();
        this.status = "closed";
        this.wantConnected = false;
        this.adapter = options.adapter ?? wechatAdapter();
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
                    for (const message of messages)
                        this.emit("message", message, source);
                },
                onMessageUpdated: (message) => this.emit("message.updated", message),
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
                if (event.added_user_ids?.length) {
                    this.emit("members.added", {
                        conversationId: event.conversation_id,
                        addedUserIds: event.added_user_ids,
                        event,
                    });
                }
                break;
            case "conversation.member_removed":
                if (event.removed_user_id !== undefined) {
                    this.emit("members.removed", {
                        conversationId: event.conversation_id,
                        removedUserId: event.removed_user_id,
                        event,
                    });
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
        return this.rest.listGroups();
    }
    createConversation(input) {
        return this.rest.createConversation(input);
    }
    createDirect(otherUserId) {
        return this.rest.createDirect(otherUserId);
    }
    createGroup(title, memberIds) {
        return this.rest.createGroup(title, memberIds);
    }
    getConversation(uuid) {
        return this.rest.getConversation(uuid);
    }
    addMembers(conversationId, memberIds) {
        return this.rest.addMembers(conversationId, memberIds);
    }
    removeMember(conversationId, userId) {
        return this.rest.removeMember(conversationId, userId);
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
     * Send a message. Resolves with the stored message; the local sequence
     * tracker is updated so the sender's own message.created event does not
     * trigger a redundant fetch. The message is also emitted via "message"
     * only when it arrives back through realtime/sync (at-least-once) — handle
     * the return value for immediate UI feedback.
     */
    async sendMessage(conversationId, content, options = {}) {
        const message = await this.rest.sendMessage(conversationId, content, options);
        this.sync.track(conversationId, message.sequence);
        return message;
    }
    lastSequence(conversationId) {
        return this.sync.lastSequence(conversationId);
    }
    /** Drop all sync state for a conversation (e.g. after being kicked). */
    forgetConversation(conversationId) {
        this.sync.forget(conversationId);
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
