"use strict";
// WebSocket gateway client (deps/im/docs/design/gateway.md):
//   - connect to wss://<gateway>/ws (ws:// for local dev)
//   - first application frame must be {"cmd":"auth","opts":["<credential>"]}
//     within the server's 10-second deadline
//   - success replies {"code":0,"data":"OK"}; failure closes with 1008
//   - {"cmd":"ping"} answers {"code":0,"data":"PONG"}; protocol-level
//     ping/pong (30s) is handled by the platform automatically
//   - server pushes at-least-once event frames; dedupe by event_id
// Never rely on the socket for missed messages — resync over HTTP
// (after_sequence) after every reconnect.
Object.defineProperty(exports, "__esModule", { value: true });
exports.GatewaySocket = void 0;
const timers_js_1 = require("./timers.js");
const MAX_SEEN_EVENTS = 10000;
const MAX_RECONNECT_DELAY_MS = 10000;
class GatewaySocket {
    constructor(options) {
        this.socket = null;
        this.seenEventIds = new Set();
        this.reconnectDelayMs = 500;
        this.closedByUser = false;
        this.authenticated = false;
        this.authRejected = false;
        /** Credential value we already tried refreshing after an auth rejection. */
        this.refreshedFor = null;
        this.pingTimer = null;
        this.reconnectTimer = null;
        this.adapter = options.adapter;
        this.url = options.wsUrl.replace(/\/+$/, "");
        this.credential = options.credential;
        this.callbacks = options.callbacks;
        this.pingIntervalMs = options.pingIntervalMs ?? 25000;
        this.getCredential = options.getCredential;
    }
    setCredential(credential) {
        this.credential = credential;
    }
    setWsUrl(wsUrl) {
        this.url = wsUrl.replace(/\/+$/, "");
    }
    get isOnline() {
        return this.authenticated;
    }
    connect() {
        if (this.socket)
            return;
        this.closedByUser = false;
        this.refreshedFor = null;
        this.open();
    }
    /** Close for good; no further reconnection until connect() is called again. */
    close() {
        this.closedByUser = true;
        this.clearTimers();
        this.socket?.close();
        this.socket = null;
        this.authenticated = false;
        this.setStatus("closed");
    }
    open() {
        this.authenticated = false;
        this.authRejected = false;
        this.setStatus(this.reconnectDelayMs > 500 ? "reconnecting" : "connecting");
        let ws;
        try {
            ws = this.adapter.socket(`${this.url}/ws`);
        }
        catch (err) {
            // The socket factory throwing means the adapter itself is unusable in
            // this runtime (e.g. the default wechatAdapter without global wx) — a
            // permanent misconfiguration, not a transient network fault. Fail fast:
            // no reconnect loop, the error propagates out of connect().
            this.setStatus("offline");
            throw err;
        }
        this.socket = ws;
        ws.onOpen(() => {
            this.setStatus("authenticating");
            ws.send(JSON.stringify({ cmd: "auth", opts: [this.credential] }));
        });
        ws.onMessage((text) => {
            let frame;
            try {
                frame = JSON.parse(text);
            }
            catch {
                return; // ignore non-JSON frames
            }
            if (!this.authenticated) {
                if (frame.code === 0) {
                    this.authenticated = true;
                    this.reconnectDelayMs = 500;
                    this.setStatus("online");
                    this.startPing();
                    this.callbacks.onReady?.();
                }
                else {
                    // Auth rejected; the gateway closes with 1008 right after. The
                    // close handler decides whether to refresh the credential or stop.
                    this.authRejected = true;
                    this.callbacks.onError?.(new Error(`gateway auth failed: ${String(frame.message ?? text)}`));
                }
                return;
            }
            if (typeof frame.cmd !== "undefined" && frame.event === undefined) {
                return; // command replies (e.g. PONG) carry no event payload
            }
            const event = frame;
            if (!event.event_id)
                return;
            if (this.seenEventIds.has(event.event_id))
                return; // at-least-once dedupe
            if (this.seenEventIds.size >= MAX_SEEN_EVENTS)
                this.seenEventIds.clear();
            this.seenEventIds.add(event.event_id);
            this.callbacks.onEvent(event);
        });
        ws.onClose(() => {
            this.stopPing();
            this.authenticated = false;
            this.socket = null;
            if (this.closedByUser) {
                this.setStatus("closed");
                return;
            }
            if (this.authRejected) {
                this.authRejected = false;
                if (this.getCredential && this.refreshedFor !== this.credential) {
                    // Refresh once per credential value; expired credentials recover
                    // automatically, permanently invalid ones stop the loop.
                    this.refreshedFor = this.credential;
                    this.refreshCredential();
                }
                else {
                    this.setStatus("offline");
                }
                return;
            }
            this.setStatus("reconnecting");
            this.scheduleReconnect();
        });
        ws.onError(() => {
            // The close event follows and drives reconnection.
        });
    }
    async refreshCredential() {
        try {
            const fresh = await this.getCredential();
            if (fresh && fresh !== this.credential) {
                this.credential = fresh;
                this.refreshedFor = null;
            }
        }
        catch (err) {
            this.callbacks.onError?.(new Error(`credential refresh failed: ${err?.message ?? err}`));
        }
        finally {
            // Reconnect either way; a second rejection with the same credential
            // stops the loop (see onClose) until connect() is called again.
            this.setStatus("reconnecting");
            this.scheduleReconnect();
        }
    }
    scheduleReconnect() {
        const delay = this.reconnectDelayMs;
        this.reconnectDelayMs = Math.min(this.reconnectDelayMs * 2, MAX_RECONNECT_DELAY_MS);
        this.reconnectTimer = timers_js_1.timers.setTimeout(() => {
            this.reconnectTimer = null;
            if (!this.closedByUser && !this.socket)
                this.open();
        }, delay);
    }
    startPing() {
        this.stopPing();
        if (this.pingIntervalMs <= 0)
            return;
        this.pingTimer = timers_js_1.timers.setInterval(() => {
            if (this.socket && this.authenticated) {
                this.socket.send(JSON.stringify({ cmd: "ping" }));
            }
        }, this.pingIntervalMs);
    }
    stopPing() {
        if (this.pingTimer)
            timers_js_1.timers.clearInterval(this.pingTimer);
        this.pingTimer = null;
    }
    clearTimers() {
        this.stopPing();
        if (this.reconnectTimer)
            timers_js_1.timers.clearTimeout(this.reconnectTimer);
        this.reconnectTimer = null;
    }
    setStatus(status) {
        this.callbacks.onStatus?.(status);
    }
}
exports.GatewaySocket = GatewaySocket;
