// WebSocket client for the Airway IM gateway.
// Protocol (docs/design/gateway.md):
//   - connect to ws://<gateway>/ws
//   - first application frame must be {"cmd":"auth","opts":["<credential>"]} within 10s
//   - success replies {"code":0,"data":"OK"}; failure closes with 1008
//   - {"cmd":"ping"} answers {"code":0,"data":"PONG"}; protocol ping/pong is automatic
//   - server pushes events: message.created / message.moderated
// Delivery is at-least-once: dedupe by event_id, resync over HTTP on reconnect.

export interface GatewayEvent {
  event_id: string;
  event: "message.created" | "message.moderated" | "conversation.member_added" | "conversation.member_removed" | string;
  message_id?: string;
  conversation_id: string;
  sequence?: number;
  added_user_ids?: number[];
  removed_user_id?: number;
  targets?: { user_ids: number[] };
}

export type GatewayStatus =
  | "connecting"
  | "authenticating"
  | "online"
  | "reconnecting"
  | "closed";

export interface GatewayCallbacks {
  onEvent: (event: GatewayEvent) => void;
  onStatus?: (status: GatewayStatus) => void;
  // Fired after every successful (re)authentication, including the first.
  // The consumer should resynchronize messages over HTTP here.
  onReady?: () => void;
  onError?: (error: Error) => void;
}

const MAX_SEEN_EVENTS = 10_000;
const APP_PING_INTERVAL_MS = 25_000;
const MAX_RECONNECT_DELAY_MS = 10_000;

export class GatewayClient {
  private readonly url: string;
  private credential: string;
  private readonly callbacks: GatewayCallbacks;
  private ws: WebSocket | null = null;
  private seenEventIds = new Set<string>();
  private reconnectDelayMs = 500;
  private closedByUser = false;
  private authenticated = false;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(url: string, credential: string, callbacks: GatewayCallbacks) {
    this.url = url;
    this.credential = credential;
    this.callbacks = callbacks;
  }

  // Update the credential used for the next (re)connection, e.g. after expiry.
  setCredential(credential: string): void {
    this.credential = credential;
  }

  connect(): void {
    if (this.ws) return;
    this.closedByUser = false;
    this.open();
  }

  close(): void {
    this.closedByUser = true;
    this.clearTimers();
    this.ws?.close();
    this.ws = null;
    this.setStatus("closed");
  }

  get isOnline(): boolean {
    return this.authenticated;
  }

  private open(): void {
    this.authenticated = false;
    this.setStatus(this.reconnectDelayMs > 500 ? "reconnecting" : "connecting");

    const ws = new WebSocket(this.url);
    this.ws = ws;

    ws.addEventListener("open", () => {
      this.setStatus("authenticating");
      ws.send(JSON.stringify({ cmd: "auth", opts: [this.credential] }));
    });

    ws.addEventListener("message", (ev) => {
      const text = typeof ev.data === "string" ? ev.data : String(ev.data);
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
          this.startAppPing();
          this.callbacks.onReady?.();
        } else {
          this.callbacks.onError?.(
            new Error(`gateway auth failed: ${String(frame.message ?? text)}`),
          );
          ws.close();
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

    ws.addEventListener("close", () => {
      this.stopAppPing();
      this.authenticated = false;
      this.ws = null;
      if (!this.closedByUser) this.scheduleReconnect();
    });

    ws.addEventListener("error", () => {
      // The close event follows and drives reconnection.
    });
  }

  private scheduleReconnect(): void {
    this.setStatus("reconnecting");
    const delay = this.reconnectDelayMs;
    this.reconnectDelayMs = Math.min(this.reconnectDelayMs * 2, MAX_RECONNECT_DELAY_MS);
    this.reconnectTimer = setTimeout(() => this.open(), delay);
  }

  private startAppPing(): void {
    this.stopAppPing();
    this.pingTimer = setInterval(() => {
      if (this.ws && this.authenticated) {
        this.ws.send(JSON.stringify({ cmd: "ping" }));
      }
    }, APP_PING_INTERVAL_MS);
  }

  private stopAppPing(): void {
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.pingTimer = null;
  }

  private clearTimers(): void {
    this.stopAppPing();
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = null;
  }

  private setStatus(status: GatewayStatus): void {
    this.callbacks.onStatus?.(status);
  }
}
