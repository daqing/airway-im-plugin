import { ErrorCode } from "./error.js";
import type { IMAdapter, FileInput } from "./adapter.js";
import type { ChatMessage, Conversation, ConversationDetails, ContentType, UploadResult, User } from "./types.js";
export interface SendMessageOptions {
    contentType?: ContentType;
    /** Reuse only when retrying the same logical request (max 128 chars). */
    idempotencyKey?: string;
    /** Automatic retries with the same idempotency key on network failures. */
    retries?: number;
}
export interface ListMessagesOptions {
    afterSequence?: number;
    /** 1-200; the backend falls back to 100 outside that range. */
    limit?: number;
}
export declare class IMHttpClient {
    private baseUrl;
    private credential;
    private readonly adapter;
    private readonly timeoutMs;
    private readonly getCredential?;
    constructor(options: {
        apiUrl: string;
        credential: string;
        adapter: IMAdapter;
        timeoutMs?: number;
        /** Called once on 401/10001 to fetch a fresh host-signed credential. */
        getCredential?: () => Promise<string>;
    });
    get apiUrl(): string;
    setCredential(credential: string): void;
    get credentialValue(): string;
    private requestOnce;
    private request;
    me(): Promise<User>;
    /** List my active group conversations (direct ones are excluded by the backend). */
    listGroups(): Promise<Conversation[]>;
    /** Create or resolve a conversation. Direct requests are get-or-create (may return 200). */
    createConversation(input: {
        kind: "direct" | "group";
        /** Other members by their stable identity uuid; the authenticated user must not be included. */
        memberUuids: string[];
        title?: string;
    }): Promise<Conversation>;
    /** Get-or-create a direct conversation with one other user, by uuid. */
    createDirect(otherUserUuid: string): Promise<Conversation>;
    /** Create a new group; the authenticated user becomes its owner. */
    createGroup(title: string | null, memberUuids: string[]): Promise<Conversation>;
    getConversation(uuid: string): Promise<ConversationDetails>;
    /** Add members (by uuid) to a group (owner/admin; idempotent for already-active members). */
    addMembers(conversationId: string, memberUuids: string[]): Promise<ConversationDetails>;
    /** Remove one member (by uuid) from a group (owner/admin; cannot remove self or the owner). */
    removeMember(conversationId: string, userUuid: string): Promise<ConversationDetails>;
    /** Ordered message page after a sequence; use for history and reconnect sync. */
    listMessages(conversationId: string, options?: ListMessagesOptions): Promise<ChatMessage[]>;
    /**
     * Send a message by conversation id. A random Idempotency-Key is generated
     * per call and reused across network-failure retries, so retry storms can
     * never duplicate a message; pass options.idempotencyKey to control it.
     */
    sendMessage(conversationId: string, content: string, options?: SendMessageOptions): Promise<ChatMessage>;
    /** Nested send variant; same semantics as sendMessage. */
    sendMessageTo(conversationId: string, content: string, options?: SendMessageOptions): Promise<ChatMessage>;
    private postMessage;
    /**
     * Upload a file (development-stage API: currently no auth middleware).
     * filePath is a WeChat local path (string) or a browser File/Blob.
     */
    uploadFile(filePath: FileInput, dir?: string): Promise<UploadResult>;
    /** Public download URL for a storage key (usable with wx.downloadFile). */
    storageUrl(key: string): string;
}
export { ErrorCode };
