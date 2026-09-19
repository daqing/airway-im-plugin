"use strict";
// Minimal helpers; the SDK targets runtimes (mini programs) that may lack
// crypto.randomUUID, URLSearchParams conveniences, etc.
Object.defineProperty(exports, "__esModule", { value: true });
exports.randomId = randomId;
exports.joinUrl = joinUrl;
exports.buildUrl = buildUrl;
/** Compact unique id for idempotency keys and internal bookkeeping. */
function randomId() {
    const time = Date.now().toString(36);
    let rand = "";
    for (let i = 0; i < 4; i += 1) {
        rand += Math.floor(Math.random() * 0x10000).toString(36).padStart(4, "0");
    }
    return `${time}${rand}`;
}
function joinUrl(base, path) {
    const trimmedBase = base.replace(/\/+$/, "");
    const trimmedPath = path.replace(/^\/+/, "");
    return `${trimmedBase}/${trimmedPath}`;
}
function buildUrl(base, path, query) {
    let url = joinUrl(base, path);
    if (query) {
        const parts = [];
        for (const [key, value] of Object.entries(query)) {
            if (value === undefined)
                continue;
            parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`);
        }
        if (parts.length > 0)
            url += `?${parts.join("&")}`;
    }
    return url;
}
