// Default adapter for browsers (Vue / React / plain JS). Pure web-platform
// APIs: fetch, WebSocket, FormData/Blob, localStorage, visibilitychange.
// Selected explicitly: createIM({ adapter: browserAdapter() }) — the default
// adapter remains the WeChat Mini Program one.

import { timers } from "./timers.js";

interface FetchResponse {
  status: number;
  text(): Promise<string>;
  blob(): Promise<object>;
}

interface BrowserWebSocket {
  send(data: string): void;
  close(): void;
  addEventListener(type: "open", cb: () => void): void;
  addEventListener(type: "message", cb: (ev: { data: unknown }) => void): void;
  addEventListener(type: "close", cb: () => void): void;
  addEventListener(type: "error", cb: () => void): void;
}

interface FormDataInstance {
  append(name: string, value: unknown): void;
}

interface BrowserLike {
  fetch(input: string, init?: {
    method?: string;
    headers?: Record<string, string>;
    body?: FormDataInstance | string;
    signal?: unknown;
  }): Promise<FetchResponse>;
  WebSocket: new (url: string) => BrowserWebSocket;
  FormData: new () => FormDataInstance;
  AbortController: new () => { signal: unknown; abort(): void };
}

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

function getLocalStorage(): StorageLike | null {
  try {
    // Read lazily through globalThis: accessing localStorage can throw
    // (storage denied), and the global is absent in non-browser runtimes.
    return (globalThis as { localStorage?: StorageLike }).localStorage ?? null;
  } catch {
    return null;
  }
}

export function browserAdapter() {
  const browser = globalThis as unknown as Partial<BrowserLike>;
  if (
    typeof browser.fetch !== "function" ||
    typeof browser.WebSocket !== "function" ||
    typeof browser.FormData !== "function" ||
    typeof browser.AbortController !== "function"
  ) {
    throw new Error(
      "browserAdapter requires a web browser runtime (fetch + WebSocket + FormData + AbortController). " +
        "For WeChat Mini Programs use the default adapter; other platforms need a custom one.",
    );
  }
  const fetchWithTimeout = (
    url: string,
    init: NonNullable<Parameters<BrowserLike["fetch"]>[1]>,
    timeoutMs: number,
  ): Promise<FetchResponse> => {
    const controller = new browser.AbortController!();
    const timer = timers.setTimeout(() => controller.abort(), timeoutMs);
    return browser
      .fetch!(url, { ...init, signal: controller.signal })
      .finally(() => timers.clearTimeout(timer));
  };

  const parseBody = async (res: FetchResponse): Promise<unknown> => {
    const text = await res.text();
    try {
      return JSON.parse(text);
    } catch {
      return text; // non-JSON body: keep raw text
    }
  };

  return {
    http: {
      async request(options: {
        url: string;
        method: "GET" | "POST" | "PUT" | "DELETE";
        headers?: Record<string, string>;
        data?: unknown;
        timeoutMs?: number;
      }): Promise<{ statusCode: number; data: unknown }> {
        const res = await fetchWithTimeout(
          options.url,
          {
            method: options.method,
            headers: options.headers,
            body: options.data !== undefined ? JSON.stringify(options.data) : undefined,
          },
          options.timeoutMs ?? 15_000,
        );
        return { statusCode: res.status, data: await parseBody(res) };
      },
    },
    socket: (url: string) => {
      const ws = new browser.WebSocket!(url);
      return {
        send: (data: string) => ws.send(data),
        close: () => ws.close(),
        onOpen: (cb: () => void) => ws.addEventListener("open", () => cb()),
        onMessage: (cb: (data: string) => void) =>
          ws.addEventListener("message", (ev) => {
            if (typeof ev.data === "string") cb(ev.data);
          }),
        onClose: (cb: () => void) => ws.addEventListener("close", () => cb()),
        onError: (cb: (err: Error) => void) =>
          ws.addEventListener("error", () => cb(new Error("socket error"))),
      };
    },
    upload: async (options: {
      url: string;
      filePath: string | object;
      name: string;
      formData?: Record<string, string>;
      timeoutMs?: number;
    }): Promise<{ statusCode: number; data: unknown }> => {
      // A string is treated as a fetchable URL (blob:/data:/https:); File and
      // Blob objects are appended directly.
      const blob =
        typeof options.filePath === "string"
          ? await (await fetchWithTimeout(options.filePath, {}, options.timeoutMs ?? 15_000)).blob()
          : options.filePath;
      const form = new browser.FormData!();
      form.append(options.name, blob);
      for (const [key, value] of Object.entries(options.formData ?? {})) {
        form.append(key, value);
      }
      const res = await fetchWithTimeout(
        options.url,
        { method: "POST", body: form },
        options.timeoutMs ?? 15_000,
      );
      return { statusCode: res.status, data: await parseBody(res) };
    },
    storage: {
      get: (key: string) => getLocalStorage()?.getItem(key) ?? null,
      set: (key: string, value: string) => {
        try {
          getLocalStorage()?.setItem(key, value);
        } catch {
          // quota exceeded or storage denied: sequence cache is best-effort
        }
      },
      remove: (key: string) => {
        try {
          getLocalStorage()?.removeItem(key);
        } catch {
          // ignore
        }
      },
    },
    onShow: (cb: () => void) => {
      // Mobile browsers drop WebSocket connections in background tabs;
      // reconnect when the tab becomes visible again.
      const doc = (globalThis as {
        document?: { addEventListener(type: string, cb: () => void): void; visibilityState: string };
      }).document;
      if (!doc) return;
      doc.addEventListener("visibilitychange", () => {
        if (doc.visibilityState === "visible") cb();
      });
    },
  };
}
