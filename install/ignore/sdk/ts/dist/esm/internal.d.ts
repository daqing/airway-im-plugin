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
/** Options for pushing one host-domain notification. */
export interface NotifyOptions {
    /** Recipient user uuids (1–100 per event; duplicates and blanks are dropped server-side). */
    userUuids: string[];
    /** Free-form event name, 1–64 characters (conventionally prefixed "host."). */
    event: string;
    /** Any JSON value, at most 4 KiB encoded; omitted becomes {}. */
    data?: unknown;
}
export interface InternalClientOptions {
    /** Internal listener URL, e.g. http://127.0.0.1:1906 — private network only. */
    internalUrl: string;
    /** The deployment's IM_INTERNAL_SECRET. */
    internalSecret: string;
    timeoutMs?: number;
}
export declare class InternalClient {
    private baseUrl;
    private readonly internalSecret;
    private readonly timeoutMs;
    constructor(options: InternalClientOptions);
    get internalUrl(): string;
    /**
     * Mint a client credential for (uuid, name) server-to-server. The user is
     * registered with IM on the first mint under that uuid (the same uuid your
     * clients pass to createDirect / sendDirectMessage); the credential carries
     * the user's current token_version, so revoking the user through the admin
     * API invalidates every credential minted before it.
     */
    mintCredential(options: MintCredentialOptions): Promise<MintedCredential>;
    /**
     * Push a host-domain notification to specific users over the IM WebSocket
     * gateway (server-to-server variant of the in-process NotifyUsers): any
     * event name the host platform defines, with a free-form JSON payload.
     * Recipients see it as the client's "host.notification" event; the four IM
     * domain events are never routed there. Limits enforced server-side: at
     * most 100 recipients per event, event names 1–64 characters, data at most
     * 4 KiB encoded. Delivery is advisory — online recipients receive it in
     * real time, offline recipients are not replayed, so pair every
     * notification with a pull endpoint clients load on demand. Resolves with
     * the generated event id.
     */
    notify(options: NotifyOptions): Promise<string>;
    private request;
}
