// airway-im-sdk-ts — Airway IM SDK for WeChat Mini Programs and
// browsers (Vue / React / plain JS).
//
// Quick start:
//   import { createClient, wechatAdapter } from "airway-im-sdk-ts";
//   const im = createClient({ apiUrl: "https://im.example.com",
//                         wsUrl: "wss://im.example.com",
//                         credential: hostIssuedCredential,
//                         adapter: wechatAdapter() });   // browsers/Node: browserAdapter()
//   await im.connect();
//   const direct = await im.createDirect(otherUserUUID);
//   direct.on("message", (msg) => console.log(msg.sender.username, msg.content));
//   await direct.send("hi");
export { AirwayIM } from "./session.js";
export { Conversation, DirectConversation, GroupConversation } from "./conversation.js";
export { IMHttpClient } from "./http.js";
export { InternalClient } from "./internal.js";
export { GatewaySocket } from "./gateway.js";
export { SyncEngine } from "./sync.js";
export { IMError, ErrorCode } from "./error.js";
export { wechatAdapter } from "./wechat.js";
export { browserAdapter } from "./browser.js";
export { randomId, joinUrl, buildUrl } from "./util.js";
import { AirwayIM } from "./session.js";
/** Create an SDK instance; adapter is required (wechatAdapter() /
 * browserAdapter() / custom). Throws when it is missing. */
export function createClient(options) {
    return new AirwayIM(options);
}
