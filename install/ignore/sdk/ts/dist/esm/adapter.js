// Platform adapter interfaces. The SDK core only talks to these interfaces;
// `wechatAdapter` (src/wechat.ts) is the default implementation for WeChat
// Mini Programs, and other platforms plug in their own (see test/node-adapter.ts
// for a Node.js one).
export {};
