// Shared types for the IM admin API surface (install/lib/im/app/api/admin_api).

export type AdminUser = {
  uuid: string;
  username: string;
  nickname: string | null;
  avatar_url: string | null;
  email: string | null;
  last_seen_at: string | null;
  token_version: number;
  created_at: string;
};

export type RevokeResult = {
  uuid: string;
  token_version: number;
  connections_kicked: number;
};

export type AdminConversation = {
  id: string;
  kind: string;
  title: string | null;
  avatar_url: string | null;
  created_by: string;
  creator_username: string;
  member_count: number;
  message_count: number;
  latest_message_at: string | null;
  created_at: string;
  updated_at: string;
};

export type AdminMessage = {
  id: string;
  conversation_id: string;
  sender_uuid: string;
  sender_username: string;
  sender_nickname: string | null;
  sender_avatar_url: string | null;
  content: string;
  content_type: string;
  sequence: number;
  created_at: string;
  is_illegal: boolean;
  moderated_at: string | null;
};

export type ServiceMetrics = {
  available: boolean;
  error: string | null;
  values: Record<string, number>;
};

export type SystemStatus = {
  generated_at: string;
  users: { total: number };
  online: { available: boolean; error: string | null; user_uuids: string[] };
  outbox_publisher: {
    pending: number;
    published: number;
    failed_attempts: number;
    oldest_pending_seconds: number;
  };
  gateway: ServiceMetrics;
  delivery: ServiceMetrics;
};
