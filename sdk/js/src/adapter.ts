// Platform adapter interfaces. The SDK core only talks to these interfaces;
// `wechatAdapter` (src/wechat.ts) is the default implementation for WeChat
// Mini Programs, and other platforms plug in their own (see test/node-adapter.ts
// for a Node.js one).

export interface HttpRequestOptions {
  url: string;
  method: "GET" | "POST" | "PUT" | "DELETE";
  headers?: Record<string, string>;
  /** JSON-serializable request body; undefined for bodyless requests. */
  data?: unknown;
  timeoutMs?: number;
}

export interface HttpResponse {
  statusCode: number;
  /** Parsed JSON value or raw string, as returned by the platform. */
  data: unknown;
}

export interface HttpAdapter {
  request(options: HttpRequestOptions): Promise<HttpResponse>;
}

export interface MiniSocket {
  send(data: string): void;
  close(): void;
  onOpen(cb: () => void): void;
  onMessage(cb: (data: string) => void): void;
  onClose(cb: () => void): void;
  onError(cb: (err: Error) => void): void;
}

export type SocketAdapterFactory = (url: string) => MiniSocket;

/**
 * Upload payload: a platform-local file path (string) on WeChat, or a
 * browser File/Blob. Kept structural (`object`) so the SDK stays independent
 * of any DOM lib; adapters narrow the type themselves.
 */
export type FileInput = string | object;

export interface UploadRequestOptions {
  url: string;
  /** WeChat local path (string) or browser File/Blob. */
  filePath: FileInput;
  /** multipart field name holding the file. */
  name: string;
  formData?: Record<string, string>;
  timeoutMs?: number;
}

export interface UploadResponse {
  statusCode: number;
  data: unknown;
}

export type UploadAdapter = (options: UploadRequestOptions) => Promise<UploadResponse>;

export interface StorageAdapter {
  get(key: string): string | null;
  set(key: string, value: string): void;
  remove(key: string): void;
}

export interface IMAdapter {
  http: HttpAdapter;
  socket: SocketAdapterFactory;
  upload?: UploadAdapter;
  storage?: StorageAdapter;
  /** Fires when the app returns to the foreground (WeChat: wx.onAppShow). */
  onShow?: (cb: () => void) => void;
}
