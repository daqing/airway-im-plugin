// Default adapter for WeChat Mini Programs, built on wx.request /
// wx.connectSocket / wx.uploadFile / wx.*StorageSync. The small local `wx`
// declaration below keeps the SDK dependency-free; it covers only the surface
// the SDK uses.

interface WxRequestOptions {
  url: string;
  method?: "GET" | "POST" | "PUT" | "DELETE" | "HEAD" | "OPTIONS" | "TRACE" | "CONNECT";
  header?: Record<string, string>;
  data?: unknown;
  timeout?: number;
  success?: (res: { statusCode: number; data: unknown }) => void;
  fail?: (err: { errMsg: string }) => void;
}

interface WxSocketTask {
  send(options: { data: string; fail?: (err: { errMsg: string }) => void }): void;
  close(options?: { code?: number; reason?: string }): void;
  onOpen(cb: () => void): void;
  onMessage(cb: (res: { data: string | ArrayBuffer }) => void): void;
  onClose(cb: (res: { code: number; reason: string }) => void): void;
  onError(cb: (res: { errMsg: string }) => void): void;
}

interface WxLike {
  request(options: WxRequestOptions): void;
  connectSocket(options: { url: string; fail?: (err: { errMsg: string }) => void }): WxSocketTask;
  uploadFile(options: {
    url: string;
    filePath: string;
    name: string;
    header?: Record<string, string>;
    formData?: Record<string, string>;
    timeout?: number;
    success?: (res: { statusCode: number; data: unknown }) => void;
    fail?: (err: { errMsg: string }) => void;
  }): void;
  getStorageSync(key: string): unknown;
  setStorageSync(key: string, value: unknown): void;
  removeStorageSync(key: string): void;
  onAppShow(cb: () => void): void;
}

function getWx(): WxLike {
  const g = globalThis as Record<string, unknown>;
  if (typeof g.wx !== "object" || g.wx === null) {
    throw new Error(
      "wechatAdapter requires the WeChat Mini Program runtime (global wx). " +
        "For other platforms, provide a custom adapter to createIM()/AirwayIM.",
    );
  }
  return g.wx as WxLike;
}

/** Platform adapter for WeChat Mini Programs — the default when no adapter is passed. */
export function wechatAdapter() {
  return {
    http: {
      request(options: {
        url: string;
        method: "GET" | "POST" | "PUT" | "DELETE";
        headers?: Record<string, string>;
        data?: unknown;
        timeoutMs?: number;
      }): Promise<{ statusCode: number; data: unknown }> {
        return new Promise((resolve, reject) => {
          getWx().request({
            url: options.url,
            method: options.method,
            header: options.headers,
            // Sending the pre-encoded string keeps serialization identical
            // across platforms (wx would otherwise encode objects itself).
            data: options.data !== undefined ? JSON.stringify(options.data) : undefined,
            timeout: options.timeoutMs,
            success: (res) => resolve({ statusCode: res.statusCode, data: res.data }),
            fail: (err) => reject(new Error(err.errMsg || "wx.request failed")),
          });
        });
      },
    },
    socket: (url: string) => {
      const task = getWx().connectSocket({ url });
      let messageCb: ((data: string) => void) | null = null;
      return {
        send: (data: string) => task.send({ data }),
        close: () => task.close({}),
        onOpen: (cb: () => void) => task.onOpen(cb),
        onMessage: (cb: (data: string) => void) => {
          messageCb = cb;
          task.onMessage((res) => {
            // The gateway only emits JSON text frames; ignore binary frames.
            if (typeof res.data === "string") messageCb?.(res.data);
          });
        },
        onClose: (cb: () => void) => task.onClose(() => cb()),
        onError: (cb: (err: Error) => void) => task.onError((res) => cb(new Error(res.errMsg || "socket error"))),
      };
    },
    upload: (options: {
      url: string;
      filePath: string | object;
      name: string;
      formData?: Record<string, string>;
      timeoutMs?: number;
    }): Promise<{ statusCode: number; data: unknown }> => {
      const filePath = options.filePath;
      if (typeof filePath !== "string") {
        return Promise.reject(
          new Error("wechatAdapter.upload expects a local file path string; use browserAdapter for File/Blob uploads"),
        );
      }
      return new Promise((resolve, reject) => {
        getWx().uploadFile({
          url: options.url,
          filePath,
          name: options.name,
          formData: options.formData,
          timeout: options.timeoutMs,
          success: (res) => resolve({ statusCode: res.statusCode, data: res.data }),
          fail: (err) => reject(new Error(err.errMsg || "wx.uploadFile failed")),
        });
      });
    },
    storage: {
      get: (key: string) => {
        try {
          const value = getWx().getStorageSync(key);
          return typeof value === "string" && value.length > 0 ? value : null;
        } catch {
          return null;
        }
      },
      set: (key: string, value: string) => {
        try {
          getWx().setStorageSync(key, value);
        } catch {
          // storage full or unavailable: sequence cache is best-effort
        }
      },
      remove: (key: string) => {
        try {
          getWx().removeStorageSync(key);
        } catch {
          // ignore
        }
      },
    },
    onShow: (cb: () => void) => {
      try {
        getWx().onAppShow(cb);
      } catch {
        // onAppShow is unavailable in some contexts (e.g. workers)
      }
    },
  };
}
