// Typed HTTP client for the Airway IM plugin REST API.
// See deps/im/docs/api/openapi.md for the contract this implements.

export interface User {
  id: number;
  uuid: string;
  username: string;
  nickname: string | null;
  avatar_url: string | null;
  email: string | null;
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface Conversation {
  id: string;
  kind: "direct" | "group";
  title: string | null;
  avatar_url: string | null;
  created_by: number;
  created_at: string;
  updated_at: string;
}

export interface ConversationMember {
  id: number;
  uuid: string;
  username: string;
  nickname: string | null;
  avatar_url: string | null;
  role: "owner" | "admin" | "member";
}

export interface ConversationDetails {
  conversation_uuid: string;
  type: "direct" | "group";
  members: ConversationMember[];
}

export interface Sender {
  id: number;
  username: string;
  nickname: string | null;
  avatar_url: string | null;
}

export interface ChatMessage {
  id: string;
  conversation_id: string;
  sender: Sender;
  content: string;
  content_type: "text/markdown" | "text/plain";
  created_at: string;
  sequence: number;
}

interface Envelope<T> {
  code: number;
  data: T;
  message: string | null;
}

export interface AdminConversation {
  id: string;
  kind: "group";
  title: string | null;
  avatar_url: string | null;
  created_by: number;
  creator_username: string;
  member_count: number;
  message_count: number;
  latest_message_at: string | null;
  created_at: string;
  updated_at: string;
}

// AdminClient wraps the /admin/api console endpoints (session-token auth).
export class AdminClient {
  private readonly baseUrl: string;
  private readonly token: string;

  private constructor(baseUrl: string, token: string) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = token;
  }

  static async login(baseUrl: string, username: string, password: string): Promise<AdminClient> {
    const res = await fetch(`${baseUrl.replace(/\/+$/, "")}/admin/api/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    const body = (await res.json()) as Envelope<{ token: string }>;
    if (!res.ok || body.code !== 0) {
      throw new IMError(body.code ?? -1, body.message ?? `HTTP ${res.status}`, res.status);
    }
    return new AdminClient(baseUrl, body.data.token);
  }

  async listAllGroups(): Promise<AdminConversation[]> {
    const res = await fetch(`${this.baseUrl}/admin/api/conversations`, {
      headers: { Authorization: `Bearer ${this.token}` },
    });
    const body = (await res.json()) as Envelope<AdminConversation[]>;
    if (!res.ok || body.code !== 0) {
      throw new IMError(body.code ?? -1, body.message ?? `HTTP ${res.status}`, res.status);
    }
    return body.data;
  }
}

export class IMError extends Error {
  readonly code: number;
  readonly status: number;

  constructor(code: number, message: string, status: number) {
    super(message);
    this.name = "IMError";
    this.code = code;
    this.status = status;
  }
}

export class IMClient {
  private readonly baseUrl: string;
  private readonly credential: string;

  private constructor(baseUrl: string, credential: string) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.credential = credential;
  }

  static withCredential(baseUrl: string, credential: string): IMClient {
    return new IMClient(baseUrl.replace(/\/+$/, ""), credential);
  }

  // Mint a credential through the server-to-server endpoint. Intended for
  // local development and demos; production Airway projects sign credentials themselves.
  // The internal API lives on its own listener, so mintUrl (default: baseUrl)
  // usually points at a different port than the public API.
  static async mint(
    baseUrl: string,
    internalSecret: string,
    identity: { uuid: string; name: string; nickname?: string },
    mintUrl?: string,
  ): Promise<IMClient> {
    const res = await fetch(`${(mintUrl ?? baseUrl).replace(/\/+$/, "")}/internal/v1/credentials`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-IM-Internal-Secret": internalSecret,
      },
      body: JSON.stringify(identity),
    });
    const body = (await res.json()) as Envelope<{ credential: string }>;
    if (!res.ok || body.code !== 0) {
      throw new IMError(body.code ?? -1, body.message ?? `HTTP ${res.status}`, res.status);
    }
    return new IMClient(baseUrl.replace(/\/+$/, ""), body.data.credential);
  }

  get credentialValue(): string {
    return this.credential;
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
    extraHeaders?: Record<string, string>,
  ): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${this.credential}`,
        ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
        ...extraHeaders,
      },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const parsed = (await res.json()) as Envelope<T>;
    if (!res.ok || parsed.code !== 0) {
      throw new IMError(parsed.code ?? -1, parsed.message ?? `HTTP ${res.status}`, res.status);
    }
    return parsed.data;
  }

  me(): Promise<User> {
    return this.request<User>("GET", "/api/v1/me");
  }

  listGroups(): Promise<Conversation[]> {
    return this.request<Conversation[]>("GET", "/api/v1/conversations?type=group");
  }

  createGroup(title: string | null, memberUuids: string[]): Promise<Conversation> {
    return this.request<Conversation>("POST", "/api/v1/group", {
      title,
      member_uuids: memberUuids,
    });
  }

  getConversation(uuid: string): Promise<ConversationDetails> {
    return this.request<ConversationDetails>(
      "GET",
      `/api/v1/conversations/${encodeURIComponent(uuid)}`,
    );
  }

  addMembers(conversationId: string, memberUuids: string[]): Promise<ConversationDetails> {
    return this.request<ConversationDetails>(
      "POST",
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/members`,
      { member_uuids: memberUuids },
    );
  }

  removeMember(conversationId: string, userUuid: string): Promise<ConversationDetails> {
    return this.request<ConversationDetails>(
      "DELETE",
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/members/${encodeURIComponent(userUuid)}`,
    );
  }

  listMessages(conversationId: string, afterSequence = 0, limit = 200): Promise<ChatMessage[]> {
    return this.request<ChatMessage[]>(
      "GET",
      `/api/v1/conversations/${encodeURIComponent(conversationId)}/messages?after_sequence=${afterSequence}&limit=${limit}`,
    );
  }

  sendMessage(
    conversationId: string,
    content: string,
    options: { contentType?: "text/markdown" | "text/plain"; idempotencyKey?: string } = {},
  ): Promise<ChatMessage> {
    return this.request<ChatMessage>(
      "POST",
      "/api/v1/messages",
      {
        conversation_id: conversationId,
        content,
        content_type: options.contentType ?? "text/markdown",
      },
      options.idempotencyKey ? { "Idempotency-Key": options.idempotencyKey } : undefined,
    );
  }
}
