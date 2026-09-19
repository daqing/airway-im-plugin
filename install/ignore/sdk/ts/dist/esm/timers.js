// Timer access without pulling in DOM/Node lib types: ES2020's globalThis
// carries setTimeout/setInterval in every runtime the SDK targets
// (WeChat mini program, Node 22+, browsers).
export const timers = globalThis;
