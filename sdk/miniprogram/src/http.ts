// Typed REST client for the Airway IM plugin public API.
// Contract: deps/im/docs/api/openapi.md — all endpoints answer with the
// {code, data, message} envelope and authenticate via
// Authorization: Bearer <host-signed credential>.

import { IMError, ErrorCode } from "./error.js";
import type { IMAdapter, FileInput } from "./adapter.js";
import { buildUrl, joinUrl, randomId } from "./util.js";
import type {
  ChatMessage,
  Conversation,
  ConversationDetails,
  ContentType,
  UploadResult,
  User,
} from "./types.js";

interface Envelope<T> {
  code: number;
  data: T;
  message: string | null;
}

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

export class IMHttpClient {
  private baseUrl: string;
  private credential: string;
  private readonly adapter: IMAdapter;
  private readonly timeoutMs: number;
  private readonly getCredential?: () => Promise<string>;

  constructor(options: {
    apiUrl: string;
    credential: string;
    adapter: IMAdapter;
    timeoutMs?: number;
    /** Called once on 401/10001 to fetch a fresh host-signed credential. */
    getCredential?: () => Promise<string>;
  }) {
    this.baseUrl = options.apiUrl.replace(/\/+$/, "");
    this.credential = options.credential;
    this.adapter = options.adapter;
    this.timeoutMs = options.timeoutMs ?? 15_000;
    this.getCredential = options.getCredential;
  }

  get apiUrl(): string {
    return this.baseUrl;
  }

  setCredential(credential: string): void {
    this.credential = credential;
  }

  get credentialValue(): string {
    return this.credential;
  }

  private async requestOnce<T>(
    method: "GET" | "POST" | "PUT" | "DELETE",
    path: string,
    options: {
      query?: Record<string, string | number | undefined>;
      body?: unknown;
      headers?: Record<string, string>;
      auth?: boolean;
    } = {},
  ): Promise<T> {
    const url = buildUrl(this.baseUrl, path, options.query);
    const headers: Record<string, string> = { ...options.headers };
    if (options.auth !== false && this.credential) {
      headers.Authorization = `Bearer ${this.credential}`;
    }
    if (options.body !== undefined) {
      headers["Content-Type"] = "application/json";
    }

    let res;
    try {
      res = await this.adapter.http.request({
        url,
        method,
        headers,
        data: options.body,
        timeoutMs: this.timeoutMs,
      });
    } catch (err) {
      // Transport failure: no HTTP status, safe to retry at a higher level.
      throw new IMError(-1, (err as Error)?.message ?? "network error", 0);
    }

    const body = res.data as Partial<Envelope<T>> | string | null;
    const envelope: Partial<Envelope<T>> | null =
      typeof body === "string" ? safeParse(body) : (body as Partial<Envelope<T>> | null);

    if (res.statusCode >= 200 && res.statusCode < 300 && envelope && envelope.code === 0) {
      return envelope.data as T;
    }
    if (envelope && typeof envelope.code === "number") {
      throw new IMError(envelope.code, envelope.message ?? `HTTP ${res.statusCode}`, res.statusCode);
    }
    throw new IMError(-1, `HTTP ${res.statusCode}: unexpected response`, res.statusCode);
  }

  private async request<T>(
    method: "GET" | "POST" | "PUT" | "DELETE",
    path: string,
    options: {
      query?: Record<string, string | number | undefined>;
      body?: unknown;
      headers?: Record<string, string>;
      auth?: boolean;
    } = {},
  ): Promise<T> {
    try {
      return await this.requestOnce<T>(method, path, options);
    } catch (err) {
      const error = err as IMError;
      // Credential expired/revoked: refresh once through the host callback and
      // retry a single time. Other errors propagate unchanged.
      if (
        !this.getCredential ||
        !error.isAuthError ||
        options.auth === false ||
        !this.credential // never auto-retry an intentionally anonymous request
      ) {
        throw error;
      }
      const fresh = await this.getCredential();
      if (!fresh || fresh === this.credential) throw error;
      this.credential = fresh;
      return await this.requestOnce<T>(method, path, options);
    }
  }

  // ---- Identity ----

  me(): Promise<User> {
    return this.request<User>("GET", "/api/v1/me");
  }

  // ---- Conversations ----

  /** List my active group conversations (direct ones are excluded by the backend). */
  listGroups(): Promise<Conversation[]> {
    return this.request<Conversation[]>("GET", "/api/v1/conversations", {
      query: { type: "group" },
    });
  }

  /** Create or resolve a conversation. Direct requests are get-or-create (may return 200). */
  createConversation(input: {
    kind: "direct" | "group";
    /** Other members; the authenticated user must not be included. */
    memberIds: number[];
    title?: string;
  }): Promise<Conversation> {
    return this.request<Conversation>("POST", "/api/v1/conversations", {
      body: {
        kind: input.kind,
        member_ids: input.memberIds,
        ...(input.title !== undefined ? { title: input.title } : {}),
      },
    });
  }

  /** Get-or-create a direct conversation with one other user. */
  createDirect(otherUserId: number): Promise<Conversation> {
    return this.createConversation({ kind: "direct", memberIds: [otherUserId] });
  }

  /** Create a new group; the authenticated user becomes its owner. */
  createGroup(title: string | null, memberIds: number[]): Promise<Conversation> {
    return this.request<Conversation>("POST", "/api/v1/group", {
      body: { title, member_ids: memberIds },
    });
  }

  getConversation(uuid: string): Promise<ConversationDetails> {
    return this.request<ConversationDetails>(
      "GET",
      `/api/v1/conversations/${encodeURIComponent(uuid)}`,
    );
  }

  /** Add members to a group (owner/admin; idempotent for already-active members). */
  addMembers(conversationId: string, memberIds: number[]): Promise<ConversationDetails> {
    return this.request<ConversationDetails>(
      "POST",
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/members`,
      { body: { member_ids: memberIds } },
    );
  }

  /** Remove one member from a group (owner/admin; cannot remove self or the owner). */
  removeMember(conversationId: string, userId: number): Promise<ConversationDetails> {
    return this.request<ConversationDetails>(
      "DELETE",
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/members/${userId}`,
    );
  }

  // ---- Messages ----

  /** Ordered message page after a sequence; use for history and reconnect sync. */
  listMessages(conversationId: string, options: ListMessagesOptions = {}): Promise<ChatMessage[]> {
    return this.request<ChatMessage[]>(
      "GET",
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/messages`,
      {
        query: {
          after_sequence: options.afterSequence,
          limit: options.limit,
        },
      },
    );
  }

  /**
   * Send a message by conversation id. A random Idempotency-Key is generated
   * per call and reused across network-failure retries, so retry storms can
   * never duplicate a message; pass options.idempotencyKey to control it.
   */
  sendMessage(
    conversationId: string,
    content: string,
    options: SendMessageOptions = {},
  ): Promise<ChatMessage> {
    return this.postMessage(
      "/api/v1/messages",
      {
        conversation_id: conversationId,
        content,
        content_type: options.contentType ?? "text/markdown",
      },
      options,
    );
  }

  /** Nested send variant; same semantics as sendMessage. */
  sendMessageTo(
    conversationId: string,
    content: string,
    options: SendMessageOptions = {},
  ): Promise<ChatMessage> {
    return this.postMessage(
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/messages`,
      {
        content,
        content_type: options.contentType ?? "text/markdown",
      },
      options,
    );
  }

  private async postMessage(
    path: string,
    body: Record<string, unknown>,
    options: SendMessageOptions,
  ): Promise<ChatMessage> {
    const key = options.idempotencyKey ?? randomId();
    const maxRetries = options.retries ?? 1;
    let attempt = 0;
    for (;;) {
      try {
        return await this.request<ChatMessage>("POST", path, {
          headers: { "Idempotency-Key": key },
          body,
        });
      } catch (err) {
        const error = err as IMError;
        attempt += 1;
        // Retry transport failures (status 0) only; HTTP errors are final and
        // the backend already dedupes by the reused idempotency key anyway.
        if (error.status !== 0 || attempt > maxRetries) throw error;
      }
    }
  }

  // ---- Storage ----

  /**
   * Upload a file (development-stage API: currently no auth middleware).
   * filePath is a WeChat local path (string) or a browser File/Blob.
   */
  async uploadFile(filePath: FileInput, dir?: string): Promise<UploadResult> {
    if (!this.adapter.upload) {
      throw new IMError(-1, "The current adapter does not support file upload", 0);
    }
    const res = await this.adapter.upload({
      url: joinUrl(this.baseUrl, "/api/v1/storage"),
      filePath,
      name: "file",
      formData: dir ? { dir } : undefined,
      timeoutMs: Math.max(this.timeoutMs, 60_000),
    });
    if (res.statusCode >= 200 && res.statusCode < 300) {
      const parsed = typeof res.data === "string" ? safeParse<UploadResult>(res.data) : (res.data as UploadResult | null);
      if (parsed && typeof parsed.key === "string") return parsed;
    }
    const parsed = typeof res.data === "string" ? safeParse<{ error?: string }>(res.data) : (res.data as { error?: string } | null);
    throw new IMError(-1, parsed?.error ?? `upload failed: HTTP ${res.statusCode}`, res.statusCode);
  }

  /** Public download URL for a storage key (usable with wx.downloadFile). */
  storageUrl(key: string): string {
    return joinUrl(this.baseUrl, `/api/v1/storage/${key.split("/").map(encodeURIComponent).join("/")}`);
  }
}

function safeParse<T = unknown>(text: string): T | null {
  try {
    return JSON.parse(text) as T;
  } catch {
    return null;
  }
}

// Re-export for convenience in host apps.
export { ErrorCode };
