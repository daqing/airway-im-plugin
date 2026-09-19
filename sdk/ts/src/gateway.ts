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

import type { IMAdapter } from "./adapter.js";
import { timers } from "./timers.js";
import type { ConnectionStatus, GatewayEvent } from "./types.js";

export interface GatewayCallbacks {
  onEvent: (event: GatewayEvent) => void;
  onStatus?: (status: ConnectionStatus) => void;
  /** Fired after every successful (re)authentication; resync over HTTP here. */
  onReady?: () => void;
  onError?: (error: Error) => void;
}

const MAX_SEEN_EVENTS = 10_000;
const MAX_RECONNECT_DELAY_MS = 10_000;

export class GatewaySocket {
  private readonly adapter: IMAdapter;
  private url: string;
  private credential: string;
  private readonly callbacks: GatewayCallbacks;
  private readonly pingIntervalMs: number;
  private readonly getCredential?: () => Promise<string>;

  private socket: ReturnType<IMAdapter["socket"]> | null = null;
  private readonly seenEventIds = new Set<string>();
  private reconnectDelayMs = 500;
  private closedByUser = false;
  private authenticated = false;
  private authRejected = false;
  /** Credential value we already tried refreshing after an auth rejection. */
  private refreshedFor: string | null = null;
  private pingTimer: number | null = null;
  private reconnectTimer: number | null = null;

  constructor(options: {
    wsUrl: string;
    credential: string;
    adapter: IMAdapter;
    callbacks: GatewayCallbacks;
    /** Application-level keepalive interval; 0 disables. Default 25s. */
    pingIntervalMs?: number;
    getCredential?: () => Promise<string>;
  }) {
    this.adapter = options.adapter;
    this.url = options.wsUrl.replace(/\/+$/, "");
    this.credential = options.credential;
    this.callbacks = options.callbacks;
    this.pingIntervalMs = options.pingIntervalMs ?? 25_000;
    this.getCredential = options.getCredential;
  }

  setCredential(credential: string): void {
    this.credential = credential;
  }

  setWsUrl(wsUrl: string): void {
    this.url = wsUrl.replace(/\/+$/, "");
  }

  get isOnline(): boolean {
    return this.authenticated;
  }

  connect(): void {
    if (this.socket) return;
    this.closedByUser = false;
    this.refreshedFor = null;
    this.open();
  }

  /** Close for good; no further reconnection until connect() is called again. */
  close(): void {
    this.closedByUser = true;
    this.clearTimers();
    this.socket?.close();
    this.socket = null;
    this.authenticated = false;
    this.setStatus("closed");
  }

  private open(): void {
    this.authenticated = false;
    this.authRejected = false;
    this.setStatus(this.reconnectDelayMs > 500 ? "reconnecting" : "connecting");

    let ws;
    try {
      ws = this.adapter.socket(`${this.url}/ws`);
    } catch (err) {
      this.callbacks.onError?.(err as Error);
      this.setStatus("offline");
      this.scheduleReconnect();
      return;
    }
    this.socket = ws;

    ws.onOpen(() => {
      this.setStatus("authenticating");
      ws.send(JSON.stringify({ cmd: "auth", opts: [this.credential] }));
    });

    ws.onMessage((text) => {
      let frame: Record<string, unknown>;
      try {
        frame = JSON.parse(text) as Record<string, unknown>;
      } catch {
        return; // ignore non-JSON frames
      }

      if (!this.authenticated) {
        if (frame.code === 0) {
          this.authenticated = true;
          this.reconnectDelayMs = 500;
          this.setStatus("online");
          this.startPing();
          this.callbacks.onReady?.();
        } else {
          // Auth rejected; the gateway closes with 1008 right after. The
          // close handler decides whether to refresh the credential or stop.
          this.authRejected = true;
          this.callbacks.onError?.(
            new Error(`gateway auth failed: ${String(frame.message ?? text)}`),
          );
        }
        return;
      }

      if (typeof frame.cmd !== "undefined" && frame.event === undefined) {
        return; // command replies (e.g. PONG) carry no event payload
      }

      const event = frame as unknown as GatewayEvent;
      if (!event.event_id) return;
      if (this.seenEventIds.has(event.event_id)) return; // at-least-once dedupe
      if (this.seenEventIds.size >= MAX_SEEN_EVENTS) this.seenEventIds.clear();
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
        } else {
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

  private async refreshCredential(): Promise<void> {
    try {
      const fresh = await this.getCredential!();
      if (fresh && fresh !== this.credential) {
        this.credential = fresh;
        this.refreshedFor = null;
      }
    } catch (err) {
      this.callbacks.onError?.(
        new Error(`credential refresh failed: ${(err as Error)?.message ?? err}`),
      );
    } finally {
      // Reconnect either way; a second rejection with the same credential
      // stops the loop (see onClose) until connect() is called again.
      this.setStatus("reconnecting");
      this.scheduleReconnect();
    }
  }

  private scheduleReconnect(): void {
    const delay = this.reconnectDelayMs;
    this.reconnectDelayMs = Math.min(this.reconnectDelayMs * 2, MAX_RECONNECT_DELAY_MS);
    this.reconnectTimer = timers.setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.closedByUser && !this.socket) this.open();
    }, delay);
  }

  private startPing(): void {
    this.stopPing();
    if (this.pingIntervalMs <= 0) return;
    this.pingTimer = timers.setInterval(() => {
      if (this.socket && this.authenticated) {
        this.socket.send(JSON.stringify({ cmd: "ping" }));
      }
    }, this.pingIntervalMs);
  }

  private stopPing(): void {
    if (this.pingTimer) timers.clearInterval(this.pingTimer);
    this.pingTimer = null;
  }

  private clearTimers(): void {
    this.stopPing();
    if (this.reconnectTimer) timers.clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private setStatus(status: ConnectionStatus): void {
    this.callbacks.onStatus?.(status);
  }
}
