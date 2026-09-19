"use strict";
// Timer access without pulling in DOM/Node lib types: ES2020's globalThis
// carries setTimeout/setInterval in every runtime the SDK targets
// (WeChat mini program, Node 22+, browsers).
Object.defineProperty(exports, "__esModule", { value: true });
exports.timers = void 0;
exports.timers = globalThis;
