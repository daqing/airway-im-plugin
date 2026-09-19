interface Timers {
    setTimeout(cb: () => void, ms?: number): number;
    clearTimeout(handle?: number): void;
    setInterval(cb: () => void, ms?: number): number;
    clearInterval(handle?: number): void;
}
export declare const timers: Timers;
export {};
