// Node.js adapter for running the SDK outside a mini program (e2e script
// and local debugging). Node >= 22 provides native fetch and WebSocket.

import type { IMAdapter } from "../src/adapter.js";

export function nodeAdapter(): IMAdapter {
  return {
    http: {
      async request(options) {
        const res = await fetch(options.url, {
          method: options.method,
          headers: options.headers,
          body:
            options.data !== undefined
              ? JSON.stringify(options.data)
              : undefined,
        });
        const text = await res.text();
        let data: unknown = text;
        try {
          data = JSON.parse(text);
        } catch {
          // leave as text
        }
        return { statusCode: res.status, data };
      },
    },
    socket: (url) => {
      const ws = new WebSocket(url);
      return {
        send: (data) => ws.send(data),
        close: () => ws.close(),
        onOpen: (cb) => ws.addEventListener("open", () => cb()),
        onMessage: (cb) =>
          ws.addEventListener("message", (ev: MessageEvent) => {
            if (typeof ev.data === "string") cb(ev.data);
          }),
        onClose: (cb) => ws.addEventListener("close", () => cb()),
        onError: (cb) =>
          ws.addEventListener("error", () => cb(new Error("socket error"))),
      };
    },
    upload: async (options) => {
      const form = new FormData();
      form.append(options.name, new Blob(["airway-im-miniprogram e2e upload"]), "e2e.txt");
      for (const [k, v] of Object.entries(options.formData ?? {})) form.append(k, v);
      const res = await fetch(options.url, { method: "POST", body: form });
      const text = await res.text();
      let data: unknown = text;
      try {
        data = JSON.parse(text);
      } catch {
        // leave as text
      }
      return { statusCode: res.status, data };
    },
    storage: (() => {
      const store = new Map<string, string>();
      return {
        get: (key) => store.get(key) ?? null,
        set: (key, value) => void store.set(key, value),
        remove: (key) => void store.delete(key),
      };
    })(),
  };
}
