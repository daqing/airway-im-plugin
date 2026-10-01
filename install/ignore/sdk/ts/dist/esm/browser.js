// Default adapter for browsers (Vue / React / plain JS). Pure web-platform
// APIs: fetch, WebSocket, FormData/Blob, localStorage, visibilitychange.
// Selected explicitly: createClient({ adapter: browserAdapter() }) — the default
// adapter remains the WeChat Mini Program one.
import { timers } from "./timers.js";
function getLocalStorage() {
    try {
        // Read lazily through globalThis: accessing localStorage can throw
        // (storage denied), and the global is absent in non-browser runtimes.
        return globalThis.localStorage ?? null;
    }
    catch {
        return null;
    }
}
export function browserAdapter() {
    const browser = globalThis;
    if (typeof browser.fetch !== "function" ||
        typeof browser.WebSocket !== "function" ||
        typeof browser.FormData !== "function" ||
        typeof browser.AbortController !== "function") {
        throw new Error("browserAdapter requires a web browser runtime (fetch + WebSocket + FormData + AbortController). " +
            "For WeChat Mini Programs pass wechatAdapter(); other platforms need a custom adapter.");
    }
    const fetchWithTimeout = (url, init, timeoutMs) => {
        const controller = new browser.AbortController();
        const timer = timers.setTimeout(() => controller.abort(), timeoutMs);
        return browser
            .fetch(url, { ...init, signal: controller.signal })
            .finally(() => timers.clearTimeout(timer));
    };
    const parseBody = async (res) => {
        const text = await res.text();
        try {
            return JSON.parse(text);
        }
        catch {
            return text; // non-JSON body: keep raw text
        }
    };
    return {
        http: {
            async request(options) {
                const res = await fetchWithTimeout(options.url, {
                    method: options.method,
                    headers: options.headers,
                    body: options.data !== undefined ? JSON.stringify(options.data) : undefined,
                }, options.timeoutMs ?? 15000);
                return { statusCode: res.status, data: await parseBody(res) };
            },
        },
        socket: (url) => {
            const ws = new browser.WebSocket(url);
            return {
                send: (data) => ws.send(data),
                close: () => ws.close(),
                onOpen: (cb) => ws.addEventListener("open", () => cb()),
                onMessage: (cb) => ws.addEventListener("message", (ev) => {
                    if (typeof ev.data === "string")
                        cb(ev.data);
                }),
                onClose: (cb) => ws.addEventListener("close", () => cb()),
                onError: (cb) => ws.addEventListener("error", () => cb(new Error("socket error"))),
            };
        },
        upload: async (options) => {
            // A string is treated as a fetchable URL (blob:/data:/https:); File and
            // Blob objects are appended directly.
            const blob = typeof options.filePath === "string"
                ? await (await fetchWithTimeout(options.filePath, {}, options.timeoutMs ?? 15000)).blob()
                : options.filePath;
            const form = new browser.FormData();
            form.append(options.name, blob);
            for (const [key, value] of Object.entries(options.formData ?? {})) {
                form.append(key, value);
            }
            const res = await fetchWithTimeout(options.url, { method: "POST", body: form }, options.timeoutMs ?? 15000);
            return { statusCode: res.status, data: await parseBody(res) };
        },
        storage: {
            get: (key) => getLocalStorage()?.getItem(key) ?? null,
            set: (key, value) => {
                try {
                    getLocalStorage()?.setItem(key, value);
                }
                catch {
                    // quota exceeded or storage denied: sequence cache is best-effort
                }
            },
            remove: (key) => {
                try {
                    getLocalStorage()?.removeItem(key);
                }
                catch {
                    // ignore
                }
            },
        },
        onShow: (cb) => {
            // Mobile browsers drop WebSocket connections in background tabs;
            // reconnect when the tab becomes visible again.
            const doc = globalThis.document;
            if (!doc)
                return;
            doc.addEventListener("visibilitychange", () => {
                if (doc.visibilityState === "visible")
                    cb();
            });
        },
    };
}
