import type { AirwayIM } from "./session.js";
import type { MembersAddedInfo, MembersRemovedInfo } from "./session.js";
import type { SendMessageOptions } from "./http.js";
import type { ChatMessage, ConversationDetails, ConversationKind } from "./types.js";
export interface ConversationEvents {
    /** New messages in this conversation: ordered, deduplicated, gap-filled. */
    message: (message: ChatMessage) => void;
    /** A previously seen message in this conversation was masked (content "***"). */
    "message.updated": (message: ChatMessage) => void;
    /** Group conversations only: members were added (includes the added users). */
    "members.added": (info: MembersAddedInfo) => void;
    /** Group conversations only: a member was removed (the kicked user is notified too). */
    "members.removed": (info: MembersRemovedInfo) => void;
}
type EventName = keyof ConversationEvents;
type Listener<K extends EventName> = ConversationEvents[K] extends (...args: infer A) => void ? (...args: A) => void : never;
export declare class Conversation {
    /** The owning facade; internal, not part of the public surface. */
    protected readonly im: AirwayIM;
    readonly id: string;
    readonly kind: ConversationKind;
    private readonly listeners;
    constructor(
    /** The owning facade; internal, not part of the public surface. */
    im: AirwayIM, id: string, kind: ConversationKind);
    /**
     * Subscribe to this conversation's events. The first "message" listener
     * starts tracking the conversation automatically: it fetches everything
     * since the last persisted cursor (history() semantics) and keeps the
     * conversation in sync from then on.
     */
    on<K extends EventName>(event: K, listener: Listener<K>): this;
    off<K extends EventName>(event: K, listener: Listener<K>): this;
    /**
     * Initial load: fetch messages since the last persisted cursor (or
     * fromSequence) and emit them here and on the global stream. Mostly
     * redundant after on("message") — that already auto-tracks — but useful
     * to await the backlog before rendering.
     */
    history(options?: {
        fromSequence?: number;
        limit?: number;
    }): Promise<ChatMessage[]>;
    /** Send a message to this conversation (idempotency semantics as sendGroupMessage). */
    send(content: string, options?: SendMessageOptions): Promise<ChatMessage>;
    /** Conversation kind plus members with roles, fresh from the API. */
    details(): Promise<ConversationDetails>;
    /** Last synced sequence in this conversation. */
    lastSequence(): number;
    /** Drop all sync state (e.g. after being kicked from the group). */
    forget(): void;
    /** @internal AirwayIM routes global engine events here. */
    emitLocal<K extends EventName>(event: K, ...args: Parameters<Listener<K>>): void;
}
/** A direct (1:1) conversation; the peer of an incoming message is msg.sender. */
export declare class DirectConversation extends Conversation {
    readonly kind: "direct";
    constructor(im: AirwayIM, id: string);
}
/** A group conversation; carries group-only operations. */
export declare class GroupConversation extends Conversation {
    readonly kind: "group";
    /** Title from creation time (null when created without one or when opened
     * by id); details() returns the live value. */
    readonly title: string | null;
    constructor(im: AirwayIM, id: string, title?: string | null);
    /** Add members by uuid (owner/admin; idempotent for active members). */
    addMembers(memberUUIDs: string[]): Promise<ConversationDetails>;
    /** Remove members by uuid (owner/admin; cannot remove self or the owner). */
    removeMembers(userUUIDs: string[]): Promise<ConversationDetails>;
}
export {};
