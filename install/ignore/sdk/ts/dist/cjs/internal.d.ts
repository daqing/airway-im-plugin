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
    private request;
}
