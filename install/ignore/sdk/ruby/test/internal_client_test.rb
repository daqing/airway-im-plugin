# frozen_string_literal: true

require "test_helper"

module AirwayIM
  class InternalClientTest < Minitest::Test
    SECRET = "internal-secret"

    def teardown
      @server&.stop
    end

    def with_server(&handler)
      @server = TestHTTPServer.new(&handler)
      InternalClient.new(internal_url: @server.url, internal_secret: SECRET)
    end

    def test_mint_credential_sends_internal_secret_and_parses_response
      credential = "im1.eyJ1dWlkIjoidXNlci00MiJ9.sig"
      client = with_server do |req|
        assert_equal "POST", req.method
        assert_equal "/internal/v1/credentials", req.path
        assert_equal SECRET, req.headers["x-im-internal-secret"]
        assert_equal({ "uuid" => "user-42", "name" => "alice", "nickname" => "Alice",
                       "ttl_seconds" => 3600 }, JSON.parse(req.body))
        [200, envelope({ credential: credential, expires_at: "2026-09-20T08:00:00Z" })]
      end

      minted = client.mint_credential(uuid: "user-42", name: "alice",
                                      nickname: "Alice", ttl_seconds: 3600)
      assert_equal credential, minted.credential
      assert_equal "2026-09-20T08:00:00Z", minted.expires_at
    end

    def test_mint_credential_omits_nil_fields
      client = with_server do |req|
        assert_equal({ "uuid" => "user-42", "name" => "alice" }, JSON.parse(req.body))
        [200, envelope({ credential: "im1.x.y", expires_at: nil })]
      end
      minted = client.mint_credential(uuid: "user-42", name: "alice")
      assert_equal "im1.x.y", minted.credential
      assert_nil minted.expires_at
    end

    def test_mint_errors_map_envelope_codes
      { [400, 10_003] => "Invalid JSON body",
        [401, 10_005] => "Internal authentication required",
        [503, 10_006] => "Credential signing is not configured" }.each do |(status, code), message|
        client = with_server do |_req|
          [status, envelope(nil, code: code, message: message)]
        end
        error = assert_raises(Error) { client.mint_credential(uuid: "u", name: "n") }
        assert_equal code, error.code
        assert_equal status, error.status
        assert_equal message, error.message
        @server.stop
      end
    end
  end
end
