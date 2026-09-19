// Minimal helpers; the SDK targets runtimes (mini programs) that may lack
// crypto.randomUUID, URLSearchParams conveniences, etc.

/** Compact unique id for idempotency keys and internal bookkeeping. */
export function randomId(): string {
  const time = Date.now().toString(36);
  let rand = "";
  for (let i = 0; i < 4; i += 1) {
    rand += Math.floor(Math.random() * 0x10000).toString(36).padStart(4, "0");
  }
  return `${time}${rand}`;
}

export function joinUrl(base: string, path: string): string {
  const trimmedBase = base.replace(/\/+$/, "");
  const trimmedPath = path.replace(/^\/+/, "");
  return `${trimmedBase}/${trimmedPath}`;
}

export function buildUrl(base: string, path: string, query?: Record<string, string | number | undefined>): string {
  let url = joinUrl(base, path);
  if (query) {
    const parts: string[] = [];
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined) continue;
      parts.push(`${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`);
    }
    if (parts.length > 0) url += `?${parts.join("&")}`;
  }
  return url;
}
