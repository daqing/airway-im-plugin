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
  end
end
