// AirwayIM: the high-level entry point combining the REST client, the
// WebSocket gateway, and the sequence-based sync engine behind one typed
// event-emitting facade. This is what mini-program apps should use.

import type { IMAdapter, FileInput } from "./adapter.js";
import { IMHttpClient } from "./http.js";
import type { SendMessageOptions } from "./http.js";
import { GatewaySocket } from "./gateway.js";
import { SyncEngine } from "./sync.js";
import { IMError } from "./error.js";
import { wechatAdapter } from "./wechat.js";
import type {
  ChatMessage,
  ConnectionStatus,
  Conversation,
  ConversationDetails,
  GatewayEvent,
  UploadResult,
  User,
} from "./types.js";

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
  /** Platform adapter; defaults to the WeChat Mini Program adapter. */
  adapter?: IMAdapter;
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
  addedUserIds: number[];
  event: GatewayEvent;
}

export interface MembersRemovedInfo {
  conversationId: string;
  removedUserId: number;
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

type Listener<K extends EventName> = SessionEvents[K] extends (...args: infer A) => void
  ? (...args: A) => void
  : never;

export class AirwayIM {
  readonly rest: IMHttpClient;
  private readonly adapter: IMAdapter;
  private readonly wsUrl?: string;
  private gateway: GatewaySocket | null = null;
  private readonly sync: SyncEngine;
  private readonly listeners = new Map<EventName, Set<Listener<EventName>>>();
  private status: ConnectionStatus = "closed";
  private wantConnected = false;

  constructor(options: AirwayIMOptions) {
    this.adapter = options.adapter ?? wechatAdapter();
    this.wsUrl = options.wsUrl;
    this.rest = new IMHttpClient({
      apiUrl: options.apiUrl,
      credential: options.credential,
      adapter: this.adapter,
      timeoutMs: options.timeoutMs,
      getCredential: options.getCredential
        ? async () => {
            const fresh = await options.getCredential!();
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
          for (const message of messages) this.emit("message", message, source);
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
              const fresh = await options.getCredential!();
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
            void this.sync.resyncAll().catch((err) => this.emit("error", err as Error));
          },
          onError: (error) => this.emit("error", error),
        },
      });
    }

    if (options.autoConnect) this.connect();

    // Mini programs kill sockets in the background: reconnect on foreground
    // if we were connected (adapter must support onShow, WeChat does).
    if (this.adapter.onShow) {
      this.adapter.onShow(() => {
        if (this.wantConnected && this.gateway) this.gateway.connect();
      });
    }
  }

  // ---- Events ----

  on<K extends EventName>(event: K, listener: Listener<K>): this {
    let set = this.listeners.get(event);
    if (!set) {
      set = new Set();
      this.listeners.set(event, set);
    }
    set.add(listener as Listener<EventName>);
    return this;
  }

  off<K extends EventName>(event: K, listener: Listener<K>): this {
    this.listeners.get(event)?.delete(listener as Listener<EventName>);
    return this;
  }

  private emit<K extends EventName>(event: K, ...args: Parameters<Listener<K>>): void {
    const set = this.listeners.get(event);
    if (!set) return;
    for (const listener of [...set]) {
      try {
        (listener as (...a: unknown[]) => void)(...args);
      } catch (err) {
        // A throwing listener must not break the sync loop.
        this.emit("error", err as Error);
      }
    }
  }

  private handleEvent(event: GatewayEvent): void {
    this.emit("event", event);
    switch (event.event) {
      case "message.created":
        this.sync.handleMessageEvent(event);
        break;
      case "message.moderated":
        void this.sync
          .handleMessageModeratedEvent(event)
          .catch((err) => this.emit("error", err as Error));
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
  connect(): void {
    if (!this.gateway) {
      this.emit("error", new IMError(-1, "No wsUrl configured; realtime is disabled", 0));
      return;
    }
    this.wantConnected = true;
    this.gateway.connect();
  }

  /** Close the gateway connection and stop reconnecting. */
  disconnect(): void {
    this.wantConnected = false;
    this.gateway?.close();
    if (this.gateway) {
      // close() already emits "closed"; keep our cached status in sync.
      this.status = "closed";
    }
  }

  get connectionStatus(): ConnectionStatus {
    return this.status;
  }

  get isOnline(): boolean {
    return this.gateway?.isOnline ?? false;
  }

  /** Replace the credential everywhere (e.g. after your own re-login flow). */
  setCredential(credential: string): void {
    this.rest.setCredential(credential);
    this.gateway?.setCredential(credential);
  }

  // ---- REST passthrough ----

  me(): Promise<User> {
    return this.rest.me();
  }

  listGroups(): Promise<Conversation[]> {
    return this.rest.listGroups();
  }

  createConversation(input: { kind: "direct" | "group"; memberIds: number[]; title?: string }): Promise<Conversation> {
    return this.rest.createConversation(input);
  }

  createDirect(otherUserId: number): Promise<Conversation> {
    return this.rest.createDirect(otherUserId);
  }

  createGroup(title: string | null, memberIds: number[]): Promise<Conversation> {
    return this.rest.createGroup(title, memberIds);
  }

  getConversation(uuid: string): Promise<ConversationDetails> {
    return this.rest.getConversation(uuid);
  }

  addMembers(conversationId: string, memberIds: number[]): Promise<ConversationDetails> {
    return this.rest.addMembers(conversationId, memberIds);
  }

  removeMember(conversationId: string, userId: number): Promise<ConversationDetails> {
    return this.rest.removeMember(conversationId, userId);
  }

  /**
   * Initial load for a conversation: fetch messages after fromSequence
   * (default: last persisted sequence, else 0), track the sequence, and emit
   * each message via the "message" event with source "history". After this,
   * the conversation is tracked and realtime events auto-heal gaps for it.
   */
  history(
    conversationId: string,
    options: { fromSequence?: number; limit?: number } = {},
  ): Promise<ChatMessage[]> {
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
  listMessages(
    conversationId: string,
    options: { afterSequence?: number; limit?: number } = {},
  ): Promise<ChatMessage[]> {
    return this.rest.listMessages(conversationId, options);
  }

  /**
   * Send a message. Resolves with the stored message; the local sequence
   * tracker is updated so the sender's own message.created event does not
   * trigger a redundant fetch. The message is also emitted via "message"
   * only when it arrives back through realtime/sync (at-least-once) — handle
   * the return value for immediate UI feedback.
   */
  async sendMessage(
    conversationId: string,
    content: string,
    options: SendMessageOptions = {},
  ): Promise<ChatMessage> {
    const message = await this.rest.sendMessage(conversationId, content, options);
    this.sync.track(conversationId, message.sequence);
    return message;
  }

  lastSequence(conversationId: string): number {
    return this.sync.lastSequence(conversationId);
  }

  /** Drop all sync state for a conversation (e.g. after being kicked). */
  forgetConversation(conversationId: string): void {
    this.sync.forget(conversationId);
  }

  // ---- Storage ----

  /** Upload a WeChat local path or browser File/Blob; returns {key,url,size}. */
  uploadFile(filePath: FileInput, dir?: string): Promise<UploadResult> {
    return this.rest.uploadFile(filePath, dir);
  }

  /** Public URL for a storage key (for <image src>, wx.downloadFile, ...). */
  storageUrl(key: string): string {
    return this.rest.storageUrl(key);
  }
}
