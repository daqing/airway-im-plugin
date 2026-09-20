import type { IMHttpClient } from "./http.js";
import type { StorageAdapter } from "./adapter.js";
import type { ChatMessage, GatewayEvent } from "./types.js";
export interface SyncHandlers {
    onMessages: (messages: ChatMessage[]) => void;
    onMessageUpdated: (message: ChatMessage) => void;
}
export declare class SyncEngine {
    private readonly http;
    private readonly handlers;
    private readonly storage?;
    private readonly storageKey;
    private readonly lastSeq;
    private readonly queues;
    constructor(options: {
        http: IMHttpClient;
        handlers: SyncHandlers;
        storage?: StorageAdapter;
        storageKey?: string;
    });
    /** Mark a conversation as tracked, raising its last-seen sequence. */
    track(conversationId: string, sequence: number): void;
    isTracked(conversationId: string): boolean;
    lastSequence(conversationId: string): number;
    forget(conversationId: string): void;
    private load;
    private persist;
    /** Serialized per-conversation fetch; returns the messages it emitted. */
    fetchFrom(conversationId: string, options?: {
        fromSequence?: number;
        limit?: number;
        maxPages?: number;
    }): Promise<ChatMessage[]>;
    /** Serialized reload of one masked (moderated) message. */
    reloadMessage(conversationId: string, event: GatewayEvent): Promise<ChatMessage | undefined>;
    /** Resynchronize every tracked conversation after a (re)connect. */
    resyncAll(): Promise<void>;
    private enqueue;
    private runFetch;
    /** Handle a gateway message.created event (serialized per conversation). */
    handleMessageEvent(event: GatewayEvent): void;
    /** Handle a gateway message.moderated event by reloading the masked message. */
    handleMessageModeratedEvent(event: GatewayEvent): Promise<void>;
}
