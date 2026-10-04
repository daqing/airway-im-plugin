# frozen_string_literal: true

module AirwayIM
  # Server-to-server client for the plugin's internal API listener
  # (IM_INTERNAL_ADDR, default 127.0.0.1:1906, secret-protected). Reachable
  # only from a private network — never from end-user clients.
  #
  # Mint credentials on behalf of your users without holding IM_AUTH_SECRET:
  #
  #   internal = AirwayIM::InternalClient.new(
  #     internal_url: "http://127.0.0.1:1906",
  #     internal_secret: ENV["IM_INTERNAL_SECRET"],
  #   )
  #   minted = internal.mint_credential(uuid: "user-42", name: "alice")
  #
  # Minted credentials carry the user's current token_version and are
  # therefore revocable through the admin API.
  #
  # The same secret also authorizes `notify_users`, which pushes host-domain
  # events (friend requests, mail alerts, …) to online users over the IM
  # WebSocket gateway.
  class InternalClient
    # A freshly minted credential. expires_at is nil for ttl_seconds: 0
    # (no expiry claim — service credentials only).
    MintedCredential = Struct.new(:credential, :expires_at, keyword_init: true)

    def initialize(internal_url:, internal_secret:, timeout: 15)
      @http = HTTP.new(internal_url, timeout: timeout)
      @internal_secret = internal_secret
    end

    # Mint a credential for (uuid, name). ttl_seconds defaults to 86400
    # (24 h) on the backend and is capped at 2592000 (30 days); 0 omits the
    # expiry claim. The user is registered (or profile-refreshed) at mint
    # time.
    def mint_credential(uuid:, name:, nickname: nil, avatar_url: nil, ttl_seconds: nil)
      body = { uuid: uuid, name: name }
      body[:nickname] = nickname unless nickname.nil?
      body[:avatar_url] = avatar_url unless avatar_url.nil?
      body[:ttl_seconds] = ttl_seconds unless ttl_seconds.nil?
      data = @http.request(:post, "/internal/v1/credentials",
                           headers: { "X-IM-Internal-Secret" => @internal_secret.to_s },
                           body: body)
      MintedCredential.new(credential: data["credential"], expires_at: data["expires_at"])
    end

    # Push a host-domain notification to specific users over the IM
    # WebSocket gateway: a free-form event name (conventionally prefixed
    # "host.", e.g. "host.friend_request") with a free-form JSON data
    # payload. Server-side limits: at most 100 recipients per call, event
    # name 1-64 characters, encoded data at most 4 KiB. Returns the
    # generated event id.
    #
    # Delivery is advisory: online recipients receive the event in real
    # time; offline recipients are not replayed, so pair notifications with
    # a pull endpoint for anything users must not miss.
    def notify_users(user_uuids:, event:, data: nil)
      body = { user_uuids: user_uuids, event: event, data: data }
      data = @http.request(:post, "/internal/v1/notify",
                           headers: { "X-IM-Internal-Secret" => @internal_secret.to_s },
                           body: body)
      data["event_id"]
    end
  end
end
