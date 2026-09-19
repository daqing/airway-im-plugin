export declare const ErrorCode: {
    readonly Internal: 10000;
    readonly InvalidCredential: 10001;
    readonly InvalidRequest: 10003;
    readonly PermissionDenied: 10005;
    readonly ConversationNotFound: 11001;
    readonly IdempotencyKeyReused: 11002;
};
export declare class IMError extends Error {
    /** Envelope business code (10000-11002); -1 when the body was not an envelope. */
    readonly code: number;
    /** HTTP status code; 0 for transport failures (network error, timeout). */
    readonly status: number;
    constructor(code: number, message: string, status: number);
    /** The credential is missing, malformed, revoked, or expired. */
    get isAuthError(): boolean;
}
