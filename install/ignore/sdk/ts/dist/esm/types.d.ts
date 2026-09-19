export interface User {
    id: number;
    uuid: string;
    username: string;
    nickname: string | null;
    avatar_url: string | null;
    email: string | null;
    last_seen_at: string | null;
    created_at: string;
    updated_at: string;
}
export interface Conversation {
    id: string;
    kind: "direct" | "group";
    title: string | null;
    avatar_url: string | null;
    created_by: number;
    created_at: string;
    updated_at: string;
}
export type MemberRole = "owner" | "admin" | "member";
export interface ConversationMember {
    id: number;
    username: string;
    nickname: string | null;
    avatar_url: string | null;
    role: MemberRole;
}
export interface ConversationDetails {
    conversation_uuid: string;
    type: "direct" | "group";
    members: ConversationMember[];
}
export interface Sender {
    id: number;
    username: string;
    nickname: string | null;
    avatar_url: string | null;
}
export type ContentType = "text/markdown" | "text/plain";
export interface ChatMessage {
    id: string;
    conversation_id: string;
    sender: Sender;
    content: string;
    content_type: ContentType;
    created_at: string;
    sequence: number;
}
export interface UploadResult {
    key: string;
    url: string;
    size: number;
}
export type GatewayEventType = "message.created" | "message.moderated" | "conversation.member_added" | "conversation.member_removed" | (string & {});
export interface GatewayEvent {
    event_id: string;
    event: GatewayEventType;
    message_id?: string;
    conversation_id: string;
    sequence?: number;
    added_user_ids?: number[];
    removed_user_id?: number;
    targets?: {
        user_ids: number[];
    };
}
export type ConnectionStatus = "connecting" | "authenticating" | "online" | "reconnecting" | "offline" | "closed";
