// Timer access without pulling in DOM/Node lib types: ES2020's globalThis
// carries setTimeout/setInterval in every runtime the SDK targets
// (WeChat mini program, Node 22+, browsers).

interface Timers {
  setTimeout(cb: () => void, ms?: number): number;
  clearTimeout(handle?: number): void;
  setInterval(cb: () => void, ms?: number): number;
  clearInterval(handle?: number): void;
}

export const timers: Timers = globalThis as unknown as Timers;
