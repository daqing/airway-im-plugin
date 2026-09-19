export { AirwayIM } from "./session.js";
export type { AirwayIMOptions, MembersAddedInfo, MembersRemovedInfo, SessionEvents } from "./session.js";
export { IMHttpClient } from "./http.js";
export type { ListMessagesOptions, SendMessageOptions } from "./http.js";
export { GatewaySocket } from "./gateway.js";
export type { GatewayCallbacks } from "./gateway.js";
export { SyncEngine } from "./sync.js";
export { IMError, ErrorCode } from "./error.js";
export { wechatAdapter } from "./wechat.js";
export { browserAdapter } from "./browser.js";
export { randomId, joinUrl, buildUrl } from "./util.js";
export type { IMAdapter, HttpAdapter, HttpRequestOptions, HttpResponse, MiniSocket, SocketAdapterFactory, StorageAdapter, UploadAdapter, FileInput, } from "./adapter.js";
export type { User, Conversation, ConversationDetails, ConversationMember, MemberRole, Sender, ChatMessage, ContentType, UploadResult, GatewayEvent, GatewayEventType, ConnectionStatus, } from "./types.js";
import { AirwayIM } from "./session.js";
import type { AirwayIMOptions } from "./session.js";
/** Create an SDK instance (defaults to the WeChat Mini Program adapter). */
export declare function createIM(options: AirwayIMOptions): AirwayIM;
