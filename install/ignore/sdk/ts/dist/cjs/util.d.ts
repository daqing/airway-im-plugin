/** Compact unique id for idempotency keys and internal bookkeeping. */
export declare function randomId(): string;
export declare function joinUrl(base: string, path: string): string;
export declare function buildUrl(base: string, path: string, query?: Record<string, string | number | undefined>): string;
