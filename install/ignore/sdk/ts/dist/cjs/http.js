"use strict";
// Typed REST client for the Airway IM plugin public API.
// Contract: deps/im/docs/api/openapi.md — all endpoints answer with the
// {code, data, message} envelope and authenticate via
// Authorization: Bearer <host-signed credential>.
Object.defineProperty(exports, "__esModule", { value: true });
exports.ErrorCode = exports.IMHttpClient = void 0;
const error_js_1 = require("./error.js");
Object.defineProperty(exports, "ErrorCode", { enumerable: true, get: function () { return error_js_1.ErrorCode; } });
const util_js_1 = require("./util.js");
class IMHttpClient {
    constructor(options) {
        this.baseUrl = options.apiUrl.replace(/\/+$/, "");
        this.credential = options.credential;
        this.adapter = options.adapter;
        this.timeoutMs = options.timeoutMs ?? 15000;
        this.getCredential = options.getCredential;
    }
    get apiUrl() {
        return this.baseUrl;
    }
    setCredential(credential) {
        this.credential = credential;
    }
    get credentialValue() {
        return this.credential;
    }
    async requestOnce(method, path, options = {}) {
        const url = (0, util_js_1.buildUrl)(this.baseUrl, path, options.query);
        const headers = { ...options.headers };
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
        }
        catch (err) {
            // Transport failure: no HTTP status, safe to retry at a higher level.
            throw new error_js_1.IMError(-1, err?.message ?? "network error", 0);
        }
        const body = res.data;
        const envelope = typeof body === "string" ? safeParse(body) : body;
        if (res.statusCode >= 200 && res.statusCode < 300 && envelope && envelope.code === 0) {
            return envelope.data;
        }
        if (envelope && typeof envelope.code === "number") {
            throw new error_js_1.IMError(envelope.code, envelope.message ?? `HTTP ${res.statusCode}`, res.statusCode);
        }
        throw new error_js_1.IMError(-1, `HTTP ${res.statusCode}: unexpected response`, res.statusCode);
    }
    async request(method, path, options = {}) {
        try {
            return await this.requestOnce(method, path, options);
        }
        catch (err) {
            const error = err;
            // Credential expired/revoked: refresh once through the host callback and
            // retry a single time. Other errors propagate unchanged.
            if (!this.getCredential ||
                !error.isAuthError ||
                options.auth === false ||
                !this.credential // never auto-retry an intentionally anonymous request
            ) {
                throw error;
            }
            const fresh = await this.getCredential();
            if (!fresh || fresh === this.credential)
                throw error;
            this.credential = fresh;
            return await this.requestOnce(method, path, options);
        }
    }
    // ---- Identity ----
    me() {
        return this.request("GET", "/api/v1/me");
    }
    // ---- Conversations ----
    /** List my active group conversations (direct ones are excluded by the backend). */
    listGroups() {
        return this.request("GET", "/api/v1/conversations", {
            query: { type: "group" },
        });
    }
    /** Create or resolve a conversation. Direct requests are get-or-create (may return 200). */
    createConversation(input) {
        return this.request("POST", "/api/v1/conversations", {
            body: {
                kind: input.kind,
                member_uuids: input.memberUuids,
                ...(input.title !== undefined ? { title: input.title } : {}),
            },
        });
    }
    /** Get-or-create a direct conversation with one other user, by uuid. */
    createDirect(otherUserUuid) {
        return this.createConversation({ kind: "direct", memberUuids: [otherUserUuid] });
    }
    /**
     * The direct conversation with one other user by uuid, or null when none
     * exists yet (read-only; createDirect get-or-creates instead). Combine with
     * listMessages to poll and display the history with that user.
     */
    async getDirectConversation(otherUserUuid) {
        try {
            return await this.request("GET", `/api/v1/conversations/direct/${encodeURIComponent(otherUserUuid)}`);
        }
        catch (err) {
            if (err instanceof error_js_1.IMError && err.code === error_js_1.ErrorCode.ConversationNotFound)
                return null;
            throw err;
        }
    }
    /** Create a new group; the authenticated user becomes its owner. */
    createGroup(title, memberUuids) {
        return this.request("POST", "/api/v1/group", {
            body: { title, member_uuids: memberUuids },
        });
    }
    getConversation(uuid) {
        return this.request("GET", `/api/v1/conversations/${encodeURIComponent(uuid)}`);
    }
    /** Add members (by uuid) to a group (owner/admin; idempotent for already-active members). */
    addMembers(conversationId, memberUuids) {
        return this.request("POST", `/api/v1/conversations/${encodeURIComponent(conversationId)}/members`, { body: { member_uuids: memberUuids } });
    }
    /** Remove one member (by uuid) from a group (owner/admin; cannot remove self or the owner). */
    removeMember(conversationId, userUuid) {
        return this.request("DELETE", `/api/v1/conversations/${encodeURIComponent(conversationId)}/members/${encodeURIComponent(userUuid)}`);
    }
    // ---- Messages ----
    /** Ordered message page after a sequence; use for history and reconnect sync. */
    listMessages(conversationId, options = {}) {
        return this.request("GET", `/api/v1/conversations/${encodeURIComponent(conversationId)}/messages`, {
            query: {
                after_sequence: options.afterSequence,
                limit: options.limit,
            },
        });
    }
    /**
     * Send a message by conversation id. A random Idempotency-Key is generated
     * per call and reused across network-failure retries, so retry storms can
     * never duplicate a message; pass options.idempotencyKey to control it.
     */
    sendMessage(conversationId, content, options = {}) {
        return this.postMessage("/api/v1/messages", {
            conversation_id: conversationId,
            content,
            content_type: options.contentType ?? "text/markdown",
        }, options);
    }
    /**
     * Send a direct message to one other user, identified by their uuid:
     * get-or-create the direct conversation, then send. Same idempotency
     * semantics as sendMessage.
     */
    async sendDirectMessage(otherUserUuid, content, options = {}) {
        const conversation = await this.createDirect(otherUserUuid);
        return this.sendMessage(conversation.id, content, options);
    }
    async postMessage(path, body, options) {
        const key = options.idempotencyKey ?? (0, util_js_1.randomId)();
        const maxRetries = options.retries ?? 1;
        let attempt = 0;
        for (;;) {
            try {
                return await this.request("POST", path, {
                    headers: { "Idempotency-Key": key },
                    body,
                });
            }
            catch (err) {
                const error = err;
                attempt += 1;
                // Retry transport failures (status 0) only; HTTP errors are final and
                // the backend already dedupes by the reused idempotency key anyway.
                if (error.status !== 0 || attempt > maxRetries)
                    throw error;
            }
        }
    }
    // ---- Storage ----
    /**
     * Upload a file (development-stage API: currently no auth middleware).
     * filePath is a WeChat local path (string) or a browser File/Blob.
     */
    async uploadFile(filePath, dir) {
        if (!this.adapter.upload) {
            throw new error_js_1.IMError(-1, "The current adapter does not support file upload", 0);
        }
        const res = await this.adapter.upload({
            url: (0, util_js_1.joinUrl)(this.baseUrl, "/api/v1/storage"),
            filePath,
            name: "file",
            formData: dir ? { dir } : undefined,
            timeoutMs: Math.max(this.timeoutMs, 60000),
        });
        if (res.statusCode >= 200 && res.statusCode < 300) {
            const parsed = typeof res.data === "string" ? safeParse(res.data) : res.data;
            if (parsed && typeof parsed.key === "string")
                return parsed;
        }
        const parsed = typeof res.data === "string" ? safeParse(res.data) : res.data;
        throw new error_js_1.IMError(-1, parsed?.error ?? `upload failed: HTTP ${res.statusCode}`, res.statusCode);
    }
    /** Public download URL for a storage key (usable with wx.downloadFile). */
    storageUrl(key) {
        return (0, util_js_1.joinUrl)(this.baseUrl, `/api/v1/storage/${key.split("/").map(encodeURIComponent).join("/")}`);
    }
}
exports.IMHttpClient = IMHttpClient;
function safeParse(text) {
    try {
        return JSON.parse(text);
    }
    catch {
        return null;
    }
}
