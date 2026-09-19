# frozen_string_literal: true

module AirwayIM
  # Client for the admin console API (/admin/api on the public listener).
  # Authenticates with IM_ADMIN_USERNAME / IM_ADMIN_PASSWORD session tokens
  # (12-hour in-memory sessions): the first authenticated call logs in
  # transparently, and a 401 mid-session triggers one re-login and retry.
  #
  #   admin = AirwayIM::AdminClient.new(
  #     api_url: "https://im.example.com",
  #     username: ENV["IM_ADMIN_USERNAME"],
  #     password: ENV["IM_ADMIN_PASSWORD"],
  #   )
  #   admin.users
  #   admin.revoke_user("user-42")
  class AdminClient
    def initialize(api_url:, username:, password:, timeout: 15)
      @http = HTTP.new(api_url, timeout: timeout)
      @username = username
      @password = password
      @token = nil
    end

    # Exchange admin credentials for a session token (also happens
    # implicitly before the first authenticated call). Returns the login
    # payload {token, expires_at, username}.
    def login
      data = @http.request(:post, "/admin/api/login",
                           body: { username: @username, password: @password })
      @token = data["token"]
      data
    end

    def logout
      request(:post, "/admin/api/logout")
      @token = nil
      nil
    end

    # Aggregated status: database counters plus live gateway/delivery
    # metrics and online user IDs.
    def status
      request(:get, "/admin/api/status")
    end

    # Registered identities, newest first, with token_version.
    def users
      request(:get, "/admin/api/users")
    end

    # Revoke a user's outstanding credentials (bumps token_version) and
    # best-effort kick their live gateway connections.
    def revoke_user(uuid)
      request(:post, "/admin/api/users/#{Util.escape_segment(uuid)}/revoke")
    end

    # All group conversations with member/message counts.
    def group_conversations
      request(:get, "/admin/api/conversations")
    end

    # All messages in a group conversation, ascending sequence order.
    def conversation_messages(conversation_id)
      request(:get, "/admin/api/conversations/#{Util.escape_segment(conversation_id)}/messages")
    end

    # Mark a message illegal (idempotent): masked to *** for clients, and a
    # message.moderated event fans out to online members.
    def mark_illegal(message_id)
      request(:post, "/admin/api/messages/#{Util.escape_segment(message_id)}/mark-illegal")
    end

    private

    def request(method, path)
      ensure_logged_in
      begin
        @http.request(method, path, headers: auth_header)
      rescue Error => e
        raise unless e.status == 401

        # The 12-hour session token expired (or was lost server-side):
        # re-login once and retry a single time.
        @token = nil
        ensure_logged_in
        @http.request(method, path, headers: auth_header)
      end
    end

    def ensure_logged_in
      login if @token.nil?
    end

    def auth_header
      { "Authorization" => "Bearer #{@token}" }
    end
  end
end
