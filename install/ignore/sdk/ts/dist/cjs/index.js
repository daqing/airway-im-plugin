"use strict";
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
Object.defineProperty(exports, "__esModule", { value: true });
exports.buildUrl = exports.joinUrl = exports.randomId = exports.browserAdapter = exports.wechatAdapter = exports.ErrorCode = exports.IMError = exports.SyncEngine = exports.GatewaySocket = exports.InternalClient = exports.IMHttpClient = exports.GroupConversation = exports.DirectConversation = exports.Conversation = exports.AirwayIM = void 0;
exports.createClient = createClient;
var session_js_1 = require("./session.js");
Object.defineProperty(exports, "AirwayIM", { enumerable: true, get: function () { return session_js_1.AirwayIM; } });
var conversation_js_1 = require("./conversation.js");
Object.defineProperty(exports, "Conversation", { enumerable: true, get: function () { return conversation_js_1.Conversation; } });
Object.defineProperty(exports, "DirectConversation", { enumerable: true, get: function () { return conversation_js_1.DirectConversation; } });
Object.defineProperty(exports, "GroupConversation", { enumerable: true, get: function () { return conversation_js_1.GroupConversation; } });
var http_js_1 = require("./http.js");
Object.defineProperty(exports, "IMHttpClient", { enumerable: true, get: function () { return http_js_1.IMHttpClient; } });
var internal_js_1 = require("./internal.js");
Object.defineProperty(exports, "InternalClient", { enumerable: true, get: function () { return internal_js_1.InternalClient; } });
var gateway_js_1 = require("./gateway.js");
Object.defineProperty(exports, "GatewaySocket", { enumerable: true, get: function () { return gateway_js_1.GatewaySocket; } });
var sync_js_1 = require("./sync.js");
Object.defineProperty(exports, "SyncEngine", { enumerable: true, get: function () { return sync_js_1.SyncEngine; } });
var error_js_1 = require("./error.js");
Object.defineProperty(exports, "IMError", { enumerable: true, get: function () { return error_js_1.IMError; } });
Object.defineProperty(exports, "ErrorCode", { enumerable: true, get: function () { return error_js_1.ErrorCode; } });
var wechat_js_1 = require("./wechat.js");
Object.defineProperty(exports, "wechatAdapter", { enumerable: true, get: function () { return wechat_js_1.wechatAdapter; } });
var browser_js_1 = require("./browser.js");
Object.defineProperty(exports, "browserAdapter", { enumerable: true, get: function () { return browser_js_1.browserAdapter; } });
var util_js_1 = require("./util.js");
Object.defineProperty(exports, "randomId", { enumerable: true, get: function () { return util_js_1.randomId; } });
Object.defineProperty(exports, "joinUrl", { enumerable: true, get: function () { return util_js_1.joinUrl; } });
Object.defineProperty(exports, "buildUrl", { enumerable: true, get: function () { return util_js_1.buildUrl; } });
const session_js_2 = require("./session.js");
/** Create an SDK instance; adapter is required (wechatAdapter() /
 * browserAdapter() / custom). Throws when it is missing. */
function createClient(options) {
    return new session_js_2.AirwayIM(options);
}
