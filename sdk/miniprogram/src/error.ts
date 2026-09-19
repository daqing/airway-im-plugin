// Error codes from the backend's {code,data,message} envelope
// (deps/im/docs/api/openapi.md ErrorEnvelope).

export const ErrorCode = {
  Internal: 10000,
  InvalidCredential: 10001,
  InvalidRequest: 10003,
  PermissionDenied: 10005,
  ConversationNotFound: 11001,
  IdempotencyKeyReused: 11002,
} as const;

export class IMError extends Error {
  /** Envelope business code (10000-11002); -1 when the body was not an envelope. */
  readonly code: number;
  /** HTTP status code; 0 for transport failures (network error, timeout). */
  readonly status: number;

  constructor(code: number, message: string, status: number) {
    super(message);
    this.name = "IMError";
    this.code = code;
    this.status = status;
  }

  /** The credential is missing, malformed, revoked, or expired. */
  get isAuthError(): boolean {
    return this.code === ErrorCode.InvalidCredential || this.status === 401;
  }
}
