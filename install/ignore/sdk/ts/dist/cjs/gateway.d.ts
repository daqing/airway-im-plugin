import type { IMAdapter } from "./adapter.js";
import type { ConnectionStatus, GatewayEvent } from "./types.js";
export interface GatewayCallbacks {
    onEvent: (event: GatewayEvent) => void;
    onStatus?: (status: ConnectionStatus) => void;
    /** Fired after every successful (re)authentication; resync over HTTP here. */
    onReady?: () => void;
    onError?: (error: Error) => void;
}
export declare class GatewaySocket {
    private readonly adapter;
    private url;
    private credential;
    private readonly callbacks;
    private readonly pingIntervalMs;
    private readonly getCredential?;
    private socket;
    private readonly seenEventIds;
    private reconnectDelayMs;
    private closedByUser;
    private authenticated;
    private authRejected;
    /** Credential value we already tried refreshing after an auth rejection. */
    private refreshedFor;
    private pingTimer;
    private reconnectTimer;
    constructor(options: {
        wsUrl: string;
        credential: string;
        adapter: IMAdapter;
        callbacks: GatewayCallbacks;
        /** Application-level keepalive interval; 0 disables. Default 25s. */
        pingIntervalMs?: number;
        getCredential?: () => Promise<string>;
    });
    setCredential(credential: string): void;
    setWsUrl(wsUrl: string): void;
    get isOnline(): boolean;
    connect(): void;
    /** Close for good; no further reconnection until connect() is called again. */
    close(): void;
    private open;
    private refreshCredential;
    private scheduleReconnect;
    private startPing;
    private stopPing;
    private clearTimers;
    private setStatus;
}
