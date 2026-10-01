// Conversation objects: one object per conversation with its own
// event emitter, so a chat window subscribes directly instead of filtering
// the global stream by conversation id. The kind lives on the object
// (DirectConversation / GroupConversation), and the first "message"
// listener starts tracking the conversation automatically. Conversation
// objects are cached per conversation id inside AirwayIM: opening the same
// conversation twice yields the same object with its listeners.
export class Conversation {
    constructor(
    /** The owning facade; internal, not part of the public surface. */
    im, id, kind) {
        this.im = im;
        this.listeners = new Map();
        this.id = id;
        this.kind = kind;
    }
    /**
     * Subscribe to this conversation's events. The first "message" listener
     * starts tracking the conversation automatically: it fetches everything
     * since the last persisted cursor (history() semantics) and keeps the
     * conversation in sync from then on.
     */
    on(event, listener) {
        let set = this.listeners.get(event);
        if (!set) {
            set = new Set();
            this.listeners.set(event, set);
        }
        const wasEmpty = set.size === 0;
        set.add(listener);
        if (wasEmpty && event === "message")
            this.im.ensureTracked(this.id);
        return this;
    }
    off(event, listener) {
        this.listeners.get(event)?.delete(listener);
        return this;
    }
    /**
     * Initial load: fetch messages since the last persisted cursor (or
     * fromSequence) and emit them here and on the global stream. Mostly
     * redundant after on("message") — that already auto-tracks — but useful
     * to await the backlog before rendering.
     */
    history(options = {}) {
        return this.im.history(this.id, options);
    }
    /** Send a message to this conversation (idempotency semantics as sendGroupMessage). */
    send(content, options = {}) {
        return this.im.rest.sendMessage(this.id, content, options);
    }
    /** Conversation kind plus members with roles, fresh from the API. */
    details() {
        return this.im.rest.getConversation(this.id);
    }
    /** Last synced sequence in this conversation. */
    lastSequence() {
        return this.im.lastSequence(this.id);
    }
    /** Drop all sync state (e.g. after being kicked from the group). */
    forget() {
        this.im.forgetConversation(this.id);
    }
    /** @internal AirwayIM routes global engine events here. */
    emitLocal(event, ...args) {
        const set = this.listeners.get(event);
        if (!set)
            return;
        for (const listener of [...set]) {
            try {
                listener(...args);
            }
            catch (err) {
                this.im.reportError(err);
            }
        }
    }
}
/** A direct (1:1) conversation; the peer of an incoming message is msg.sender. */
export class DirectConversation extends Conversation {
    constructor(im, id) {
        super(im, id, "direct");
    }
}
/** A group conversation; carries group-only operations. */
export class GroupConversation extends Conversation {
    constructor(im, id, title = null) {
        super(im, id, "group");
        this.title = title;
    }
    /** Add members by uuid (owner/admin; idempotent for active members). */
    addMembers(memberUUIDs) {
        return this.im.addMembers(this.id, memberUUIDs);
    }
    /** Remove members by uuid (owner/admin; cannot remove self or the owner). */
    removeMembers(userUUIDs) {
        return this.im.removeMembers(this.id, userUUIDs);
    }
}
