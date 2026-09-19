// Default adapter for WeChat Mini Programs, built on wx.request /
// wx.connectSocket / wx.uploadFile / wx.*StorageSync. The small local `wx`
// declaration below keeps the SDK dependency-free; it covers only the surface
// the SDK uses.
function getWx() {
    const g = globalThis;
    if (typeof g.wx !== "object" || g.wx === null) {
        throw new Error("wechatAdapter requires the WeChat Mini Program runtime (global wx). " +
            "For other platforms, provide a custom adapter to createIM()/AirwayIM.");
    }
    return g.wx;
}
/** Platform adapter for WeChat Mini Programs — the default when no adapter is passed. */
export function wechatAdapter() {
    return {
        http: {
            request(options) {
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
        socket: (url) => {
            const task = getWx().connectSocket({ url });
            let messageCb = null;
            return {
                send: (data) => task.send({ data }),
                close: () => task.close({}),
                onOpen: (cb) => task.onOpen(cb),
                onMessage: (cb) => {
                    messageCb = cb;
                    task.onMessage((res) => {
                        // The gateway only emits JSON text frames; ignore binary frames.
                        if (typeof res.data === "string")
                            messageCb?.(res.data);
                    });
                },
                onClose: (cb) => task.onClose(() => cb()),
                onError: (cb) => task.onError((res) => cb(new Error(res.errMsg || "socket error"))),
            };
        },
        upload: (options) => {
            const filePath = options.filePath;
            if (typeof filePath !== "string") {
                return Promise.reject(new Error("wechatAdapter.upload expects a local file path string; use browserAdapter for File/Blob uploads"));
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
            get: (key) => {
                try {
                    const value = getWx().getStorageSync(key);
                    return typeof value === "string" && value.length > 0 ? value : null;
                }
                catch {
                    return null;
                }
            },
            set: (key, value) => {
                try {
                    getWx().setStorageSync(key, value);
                }
                catch {
                    // storage full or unavailable: sequence cache is best-effort
                }
            },
            remove: (key) => {
                try {
                    getWx().removeStorageSync(key);
                }
                catch {
                    // ignore
                }
            },
        },
        onShow: (cb) => {
            try {
                getWx().onAppShow(cb);
            }
            catch {
                // onAppShow is unavailable in some contexts (e.g. workers)
            }
        },
    };
}
