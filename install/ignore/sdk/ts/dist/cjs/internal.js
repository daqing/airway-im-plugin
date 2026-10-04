"use strict";
// Server-to-server client for the plugin's internal API listener
// (IM_INTERNAL_ADDR, default 127.0.0.1:1906, X-IM-Internal-Secret protected).
// Reachable only from a private network — never from end-user clients:
// whoever can mint credentials can impersonate any user, so do not bundle
// this class into browser or Mini Program code. Requires a runtime with
// global fetch (Node 18+, Deno, Bun, modern edge runtimes).
Object.defineProperty(exports, "__esModule", { value: true });
exports.InternalClient = void 0;
const error_js_1 = require("./error.js");
const util_js_1 = require("./util.js");
const timers_js_1 = require("./timers.js");
class InternalClient {
    constructor(options) {
        this.baseUrl = options.internalUrl.replace(/\/+$/, "");
        this.internalSecret = options.internalSecret;
        this.timeoutMs = options.timeoutMs ?? 15000;
    }
    get internalUrl() {
        return this.baseUrl;
    }
    /**
     * Mint a client credential for (uuid, name) server-to-server. The user is
     * registered with IM on the first mint under that uuid (the same uuid your
     * clients pass to createDirect / sendDirectMessage); the credential carries
     * the user's current token_version, so revoking the user through the admin
     * API invalidates every credential minted before it.
     */
    async mintCredential(options) {
        const body = { uuid: options.uuid, name: options.name };
        if (options.nickname !== undefined)
            body.nickname = options.nickname;
        if (options.avatarUrl !== undefined)
            body.avatar_url = options.avatarUrl;
        if (options.ttlSeconds !== undefined)
            body.ttl_seconds = options.ttlSeconds;
        const data = await this.request("/internal/v1/credentials", body);
        if (typeof data.credential !== "string") {
            throw new error_js_1.IMError(-1, "unexpected response: missing credential", 0);
        }
        return {
            credential: data.credential,
            expiresAt: typeof data.expires_at === "string" ? data.expires_at : null,
        };
    }
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
    async notify(options) {
        const body = {
            user_uuids: options.userUuids,
            event: options.event,
        };
        if (options.data !== undefined)
            body.data = options.data;
        const data = await this.request("/internal/v1/notify", body);
        if (typeof data.event_id !== "string") {
            throw new error_js_1.IMError(-1, "unexpected response: missing event_id", 0);
        }
        return data.event_id;
    }
    async request(path, body) {
        const fetchImpl = globalThis.fetch;
        if (typeof fetchImpl !== "function") {
            throw new error_js_1.IMError(-1, "InternalClient requires a runtime with global fetch (Node 18+)", 0);
        }
        const controllerFactory = globalThis.AbortController;
        const controller = controllerFactory ? new controllerFactory() : null;
        const timer = controller ? timers_js_1.timers.setTimeout(() => controller.abort(), this.timeoutMs) : undefined;
        let res;
        try {
            res = await fetchImpl((0, util_js_1.joinUrl)(this.baseUrl, path), {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                    "X-IM-Internal-Secret": this.internalSecret,
                },
                body: JSON.stringify(body),
                signal: controller?.signal,
            });
        }
        catch (err) {
            // Transport failure (including timeout abort): no HTTP status, the
            // caller may retry.
            throw new error_js_1.IMError(-1, err?.message ?? "network error", 0);
        }
        finally {
            if (timer !== undefined)
                timers_js_1.timers.clearTimeout(timer);
        }
        const text = await res.text();
        let envelope = null;
        try {
            envelope = JSON.parse(text);
        }
        catch {
            envelope = null;
        }
        if (res.status >= 200 && res.status < 300 &&
            envelope && envelope.code === 0 &&
            envelope.data && typeof envelope.data === "object") {
            return envelope.data;
        }
        if (envelope && typeof envelope.code === "number") {
            throw new error_js_1.IMError(envelope.code, envelope.message ?? `HTTP ${res.status}`, res.status);
        }
        throw new error_js_1.IMError(-1, `HTTP ${res.status}: unexpected response`, res.status);
    }
}
exports.InternalClient = InternalClient;
