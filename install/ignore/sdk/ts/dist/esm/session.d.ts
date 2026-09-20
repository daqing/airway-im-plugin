import type { IMAdapter, FileInput } from "./adapter.js";
import { IMHttpClient } from "./http.js";
import type { SendMessageOptions } from "./http.js";
import { DirectConversation, GroupConversation } from "./conversation.js";
import type { Conversation } from "./conversation.js";
import type { ChatMessage, ConnectionStatus, ConversationSummary, ConversationDetails, GatewayEvent, UploadResult, User } from "./types.js";
export interface AirwayIMOptions {
    /** IM backend base URL, e.g. https://im.example.com (the :1905 service). */
    apiUrl: string;
    /** Gateway WebSocket base URL, e.g. wss://im.example.com (the :1910 service). Omit for REST-only use. */
    wsUrl?: string;
    /** Host-signed credential issued by your own backend. */
    credential: string;
    /**
     * Called once when the credential is rejected (HTTP 401/10001 or gateway
     * auth failure) to fetch a fresh one from your backend — e.g. via your
     * login session. Keep it fast; realtime resumes automatically with the
     * returned credential.
     */
    getCredential?: () => Promise<string>;
    /**
     * Platform adapter — required and never defaulted, so a program running in
     * the wrong runtime fails loudly here instead of mysteriously later. Pass
     * wechatAdapter() in WeChat Mini Programs, browserAdapter() in browsers and
     * Node, or your own IMAdapter implementation.
     */
    adapter: IMAdapter;
    /** Per-request timeout in ms (default 15000). */
    timeoutMs?: number;
    /** Gateway application-level ping interval in ms; 0 disables (default 25000). */
    pingIntervalMs?: number;
    /** Persist per-conversation last-seen sequences via adapter.storage (default true). */
    persistSequences?: boolean;
    /** Connect the gateway immediately (default false; call connect() yourself). */
    autoConnect?: boolean;
}
export interface MembersAddedInfo {
    conversationId: string;
    addedUserUUIDs: string[];
    event: GatewayEvent;
}
export interface MembersRemovedInfo {
    conversationId: string;
    removedUserUUID: string;
    event: GatewayEvent;
}
export interface SessionEvents {
    /** New messages, deduplicated and ordered; includes your own sends. */
    message: (message: ChatMessage, source: "history" | "realtime") => void;
    /** A previously seen message was masked by moderation (content "***"). */
    "message.updated": (message: ChatMessage) => void;
    "members.added": (info: MembersAddedInfo) => void;
    "members.removed": (info: MembersRemovedInfo) => void;
    status: (status: ConnectionStatus) => void;
    error: (error: Error) => void;
    /** Every raw gateway frame, including events for untracked conversations. */
    event: (event: GatewayEvent) => void;
}
type EventName = keyof SessionEvents;
type Listener<K extends EventName> = SessionEvents[K] extends (...args: infer A) => void ? (...args: A) => void : never;
export declare class AirwayIM {
    readonly rest: IMHttpClient;
    private readonly adapter;
    private readonly wsUrl?;
    private gateway;
    private readonly sync;
    private readonly listeners;
    private status;
    private wantConnected;
    /** conversation id → kind, remembered from every call/event that reveals it. */
    private readonly conversationKinds;
    /** conversation id → conversation object; the same object (with its listeners) is
     * returned by every open/create call for that conversation. */
    private readonly conversations;
    private rememberKind;
    private conversationFor;
    constructor(options: AirwayIMOptions);
    on<K extends EventName>(event: K, listener: Listener<K>): this;
    off<K extends EventName>(event: K, listener: Listener<K>): this;
    private emit;
    private handleEvent;
    /** Open the gateway connection (no-op in REST-only mode). */
    connect(): void;
    /** Close the gateway connection and stop reconnecting. */
    disconnect(): void;
    get connectionStatus(): ConnectionStatus;
    get isOnline(): boolean;
    /** Replace the credential everywhere (e.g. after your own re-login flow). */
    setCredential(credential: string): void;
    me(): Promise<User>;
    listGroups(): Promise<ConversationSummary[]>;
    /** Get-or-create the direct (1:1) conversation with one user, by uuid. */
    createDirect(otherUserUUID: string): Promise<DirectConversation>;
    /**
     * The existing direct conversation with one user, by uuid, as a
     * DirectConversation —
     * or null when none exists yet (createDirect get-or-creates instead).
     */
    getDirect(otherUserUUID: string): Promise<DirectConversation | null>;
    /** Create a group conversation; the authenticated user becomes its owner. */
    createGroup(title: string | null, memberUUIDs: string[]): Promise<GroupConversation>;
    /**
     * Open any conversation by id as a conversation object (e.g. one learned
     * from a "message" event or listGroups). The kind is answered from the
     * registry when this instance already saw the conversation, otherwise
     * fetched via REST once; rejects if the user cannot see the conversation.
     */
    openConversation(conversationId: string): Promise<Conversation>;
    addMembers(conversationId: string, memberUUIDs: string[]): Promise<ConversationDetails>;
    removeMembers(conversationId: string, userUUIDs: string[]): Promise<ConversationDetails>;
    /**
     * Initial load for a conversation: fetch messages after fromSequence
     * (default: last persisted sequence, else 0), track the sequence, and emit
     * each message via the "message" event with source "history". After this,
     * the conversation is tracked and realtime events auto-heal gaps for it.
     */
    history(conversationId: string, options?: {
        fromSequence?: number;
        limit?: number;
    }): Promise<ChatMessage[]>;
    /**
     * Raw ordered message page after a sequence (no state changes, no event
     * emission). Most apps should use history() + realtime events instead.
     */
    listMessages(conversationId: string, options?: {
        afterSequence?: number;
        limit?: number;
    }): Promise<ChatMessage[]>;
    /**
     * Send a message to a group conversation by id. Resolves with the stored
     * message; the local sequence tracker is updated so the sender's own
     * message.created event does not trigger a redundant fetch. The message is
     * also emitted via "message" only when it arrives back through
     * realtime/sync (at-least-once) — handle the return value for immediate UI
     * feedback.
     */
    sendGroupMessage(conversationId: string, content: string, options?: SendMessageOptions): Promise<ChatMessage>;
    /**
     * Send a direct message to one other user, identified by their uuid:
     * get-or-create the direct conversation, then send. Same idempotency
     * semantics as sendGroupMessage.
     */
    sendDirectMessage(otherUserUUID: string, content: string, options?: SendMessageOptions): Promise<ChatMessage>;
    lastSequence(conversationId: string): number;
    /** Drop all sync state for a conversation (e.g. after being kicked). */
    forgetConversation(conversationId: string): void;
    /** @internal Conversation objects: start tracking on first message listener. */
    ensureTracked(conversationId: string): void;
    /** @internal Conversation objects: surface listener exceptions. */
    reportError(err: Error): void;
    /** Upload a WeChat local path or browser File/Blob; returns {key,url,size}. */
    uploadFile(filePath: FileInput, dir?: string): Promise<UploadResult>;
    /** Public URL for a storage key (for <image src>, wx.downloadFile, ...). */
    storageUrl(key: string): string;
}
export {};
