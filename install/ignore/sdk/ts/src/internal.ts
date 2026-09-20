// Server-to-server client for the plugin's internal API listener
// (IM_INTERNAL_ADDR, default 127.0.0.1:1906, X-IM-Internal-Secret protected).
// Reachable only from a private network — never from end-user clients:
// whoever can mint credentials can impersonate any user, so do not bundle
// this class into browser or Mini Program code. Requires a runtime with
// global fetch (Node 18+, Deno, Bun, modern edge runtimes).

import { IMError } from "./error.js";
import { joinUrl } from "./util.js";
import { timers } from "./timers.js";

/** Options for minting one credential. Optional display fields are
 * write-only-when-present: omitting them never clobbers a richer profile
 * stored earlier in the IM users table. */
export interface MintCredentialOptions {
  /** Stable user id from your own user table (1-64 characters). */
  uuid: string;
  /** Account handle (1-64 characters). */
  name: string;
  nickname?: string;
  avatarUrl?: string;
  /** Lifetime in seconds; the backend defaults to 86400 (24 h), caps at
   * 2592000 (30 days); 0 omits the expiry claim (service credentials). */
  ttlSeconds?: number;
}

/** A freshly minted credential. */
export interface MintedCredential {
  /** The finished im1.<payload>.<sig> token: hand it to the client with your
   * login response and pass it to createClient({ credential }) / setCredential(). */
  credential: string;
  /** RFC 3339 UTC timestamp; null when ttlSeconds is 0 (no expiry claim). */
  expiresAt: string | null;
}

export interface InternalClientOptions {
  /** Internal listener URL, e.g. http://127.0.0.1:1906 — private network only. */
  internalUrl: string;
  /** The deployment's IM_INTERNAL_SECRET. */
  internalSecret: string;
  timeoutMs?: number;
}

interface InternalFetch {
  (input: string, init?: {
    method?: string;
    headers?: Record<string, string>;
    body?: string;
    signal?: unknown;
  }): Promise<{ status: number; text(): Promise<string> }>;
}

interface AbortControllerLike {
  signal: unknown;
  abort(): void;
}

interface Envelope {
  code?: number;
  data?: { credential?: string; expires_at?: string } | null;
  message?: string | null;
}

export class InternalClient {
  private baseUrl: string;
  private readonly internalSecret: string;
  private readonly timeoutMs: number;

  constructor(options: InternalClientOptions) {
    this.baseUrl = options.internalUrl.replace(/\/+$/, "");
    this.internalSecret = options.internalSecret;
    this.timeoutMs = options.timeoutMs ?? 15_000;
  }

  get internalUrl(): string {
    return this.baseUrl;
  }

  /**
   * Mint a client credential for (uuid, name) server-to-server. The user is
   * registered with IM on the first mint under that uuid (the same uuid your
   * clients pass to createDirect / sendDirectMessage); the credential carries
   * the user's current token_version, so revoking the user through the admin
   * API invalidates every credential minted before it.
   */
  async mintCredential(options: MintCredentialOptions): Promise<MintedCredential> {
    const body: Record<string, unknown> = { uuid: options.uuid, name: options.name };
    if (options.nickname !== undefined) body.nickname = options.nickname;
    if (options.avatarUrl !== undefined) body.avatar_url = options.avatarUrl;
    if (options.ttlSeconds !== undefined) body.ttl_seconds = options.ttlSeconds;

    const data = await this.request("/internal/v1/credentials", body);
    return {
      credential: data.credential as string,
      expiresAt: typeof data.expires_at === "string" ? data.expires_at : null,
    };
  }

  private async request(path: string, body: Record<string, unknown>): Promise<NonNullable<Envelope["data"]>> {
    const fetchImpl = (globalThis as { fetch?: InternalFetch }).fetch;
    if (typeof fetchImpl !== "function") {
      throw new IMError(-1, "InternalClient requires a runtime with global fetch (Node 18+)", 0);
    }
    const controllerFactory = (globalThis as { AbortController?: new () => AbortControllerLike }).AbortController;
    const controller = controllerFactory ? new controllerFactory() : null;
    const timer = controller ? timers.setTimeout(() => controller.abort(), this.timeoutMs) : undefined;
    let res;
    try {
      res = await fetchImpl(joinUrl(this.baseUrl, path), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-IM-Internal-Secret": this.internalSecret,
        },
        body: JSON.stringify(body),
        signal: controller?.signal,
      });
    } catch (err) {
      // Transport failure (including timeout abort): no HTTP status, the
      // caller may retry.
      throw new IMError(-1, (err as Error)?.message ?? "network error", 0);
    } finally {
      if (timer !== undefined) timers.clearTimeout(timer);
    }

    const text = await res.text();
    let envelope: Envelope | null = null;
    try {
      envelope = JSON.parse(text) as Envelope;
    } catch {
      envelope = null;
    }
    if (
      res.status >= 200 && res.status < 300 &&
      envelope && envelope.code === 0 &&
      envelope.data && typeof envelope.data.credential === "string"
    ) {
      return envelope.data;
    }
    if (envelope && typeof envelope.code === "number") {
      throw new IMError(envelope.code, envelope.message ?? `HTTP ${res.status}`, res.status);
    }
    throw new IMError(-1, `HTTP ${res.status}: unexpected response`, res.status);
  }
}
