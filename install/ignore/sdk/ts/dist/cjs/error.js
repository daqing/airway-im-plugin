"use strict";
// Error codes from the backend's {code,data,message} envelope
// (deps/im/docs/api/openapi.md ErrorEnvelope).
Object.defineProperty(exports, "__esModule", { value: true });
exports.IMError = exports.ErrorCode = void 0;
exports.ErrorCode = {
    Internal: 10000,
    InvalidCredential: 10001,
    InvalidRequest: 10003,
    PermissionDenied: 10005,
    ConversationNotFound: 11001,
    IdempotencyKeyReused: 11002,
};
class IMError extends Error {
    constructor(code, message, status) {
        super(message);
        this.name = "IMError";
        this.code = code;
        this.status = status;
    }
    /** The credential is missing, malformed, revoked, or expired. */
    get isAuthError() {
        return this.code === exports.ErrorCode.InvalidCredential || this.status === 401;
    }
}
exports.IMError = IMError;
