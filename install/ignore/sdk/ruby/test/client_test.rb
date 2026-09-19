# frozen_string_literal: true

require "stringio"
require "tempfile"
require "test_helper"

module AirwayIM
  class ClientTest < Minitest::Test
    CREDENTIAL = "im1.eyJ1dWlkIjoidXNlci00MiJ9.aaa"
    FRESH_CREDENTIAL = "im1.eyJ1dWlkIjoidXNlci00MiJ9.fresh"

    def teardown
      @server&.stop
    end

    def with_server(&handler)
      @server = TestHTTPServer.new(&handler)
      Client.new(api_url: @server.url, credential: CREDENTIAL)
    end

    def test_me_sends_bearer_token_and_unwraps_envelope
      user = { "id" => 1, "uuid" => "user-42", "username" => "alice", "nickname" => "Alice" }
      client = with_server do |req|
        assert_equal "GET", req.method
        assert_equal "/api/v1/me", req.path
        assert_equal "Bearer #{CREDENTIAL}", req.headers["authorization"]
        [200, envelope(user)]
      end
      assert_equal user, client.me
    end

    def test_list_groups_builds_type_query
      client = with_server do |req|
        assert_equal "GET", req.method
        assert_equal "/api/v1/conversations?type=group", req.path
        [200, envelope([])]
      end
      assert_equal [], client.list_groups
    end

    def test_create_direct_posts_get_or_create_body
      conversation = { "id" => "01AB", "kind" => "direct" }
      client = with_server do |req|
        assert_equal "POST", req.method
        assert_equal "/api/v1/conversations", req.path
        assert_equal({ "kind" => "direct", "member_uuids" => ["uuid-bob"] }, JSON.parse(req.body))
        [201, envelope(conversation)]
      end
      assert_equal conversation, client.create_direct("uuid-bob")
    end

    def test_create_group_omits_nil_title
      client = with_server do |req|
        body = JSON.parse(req.body)
        if req.path == "/api/v1/group"
          assert_equal({ "member_uuids" => %w[uuid-bob uuid-carol] }, body)
        else
          assert_equal({ "kind" => "group", "member_uuids" => %w[uuid-bob uuid-carol] }, body)
        end
        [201, envelope({ "id" => "01AC", "kind" => "group" })]
      end
      client.create_group(member_uuids: %w[uuid-bob uuid-carol])
      client.create_conversation(kind: "group", member_uuids: %w[uuid-bob uuid-carol])
    end

    def test_direct_conversation_returns_existing_conversation
      conversation = { "id" => "01AB", "kind" => "direct", "title" => nil }
      client = with_server do |req|
        assert_equal "GET", req.method
        assert_equal "/api/v1/conversations/direct/uuid-bob", req.path
        [200, envelope(conversation)]
      end
      assert_equal conversation, client.direct_conversation("uuid-bob")
    end

    def test_direct_conversation_returns_nil_when_absent
      client = with_server do |_req|
        [404, envelope(nil, code: 11_001, message: "Conversation not found")]
      end
      assert_nil client.direct_conversation("uuid-bob")
    end

    def test_direct_conversation_raises_other_errors
      client = with_server do |_req|
        [500, envelope(nil, code: 10_000, message: "boom")]
      end
      error = assert_raises(Error) { client.direct_conversation("uuid-bob") }
      assert_equal 10_000, error.code
    end

    def test_conversation_and_member_management_escape_ids
      client = with_server do |req|
        case req.method
        when "GET" then assert_equal "/api/v1/conversations/01%20AB", req.path
        when "POST" then assert_equal "/api/v1/conversations/01%20AB/members", req.path
        when "DELETE" then assert_equal "/api/v1/conversations/01%20AB/members/uuid%20carol", req.path
        end
        [200, envelope({ "conversation_uuid" => "01 AB", "type" => "group", "members" => [] })]
      end
      client.conversation("01 AB")
      client.add_members("01 AB", ["uuid-carol"])
      client.remove_member("01 AB", "uuid carol")
    end

    def test_messages_sends_sequence_query
      client = with_server do |req|
        assert_equal "/api/v1/conversations/01AB/messages?after_sequence=42&limit=10", req.path
        [200, envelope([])]
      end
      assert_equal [], client.messages("01AB", after_sequence: 42, limit: 10)
    end

    def test_messages_without_options_has_no_query
      client = with_server do |req|
        assert_equal "/api/v1/conversations/01AB/messages", req.path
        [200, envelope([])]
      end
      client.messages("01AB")
    end

    def test_each_message_pages_until_short_page
      client = with_server do |req|
        assert_equal "GET", req.method
        after_sequence = Integer(req.path[/after_sequence=(\d+)/, 1])
        assert_equal 2, Integer(req.path[/limit=(\d+)/, 1])
        if after_sequence.zero?
          [200, envelope([msg(1), msg(2)])]
        elsif after_sequence == 2
          [200, envelope([msg(3)])]
        else
          raise "unexpected cursor #{after_sequence}"
        end
      end

      collected = client.each_message("01AB", page_size: 2).to_a
      assert_equal [1, 2, 3], collected.map { |m| m["sequence"] }
    end

    def test_each_message_stops_on_empty_page
      client = with_server do |_req|
        [200, envelope([])]
      end
      assert_equal [], client.each_message("01AB", after_sequence: 99).to_a
    end

    def test_send_message_generates_idempotency_key
      message = { "id" => "01M1", "sequence" => 1, "content" => "hi" }
      client = with_server do |req|
        assert_equal "POST", req.method
        assert_equal "/api/v1/messages", req.path
        key = req.headers["idempotency-key"]
        assert key && key.length.between?(1, 128)
        assert_equal({ "conversation_id" => "01AB", "content" => "hi",
                       "content_type" => "text/markdown" }, JSON.parse(req.body))
        [201, envelope(message)]
      end
      assert_equal message, client.send_message("01AB", "hi")
    end

    def test_send_message_retries_transport_failure_with_same_key
      attempts = 0
      client = with_server do |req|
        attempts += 1
        raise TestHTTPServer::Abort if attempts == 1
        [201, envelope({ "id" => "01M1", "sequence" => 1 })]
      end

      message = client.send_message("01AB", "hi")
      assert_equal "01M1", message["id"]
      assert_equal 2, attempts
      assert_equal @server.requests.map { |r| r.headers["idempotency-key"] }.uniq.size, 1
    end

    def test_send_message_does_not_retry_http_errors
      client = with_server do |_req|
        [409, envelope(nil, code: 11_002, message: "Idempotency key was reused with a different request")]
      end
      error = assert_raises(Error) { client.send_message("01AB", "hi") }
      assert_equal 11_002, error.code
      assert_equal 409, error.status
      assert_equal 1, @server.requests.size
    end

    def test_send_message_honors_explicit_key_and_content_type
      client = with_server do |req|
        assert_equal "my-key", req.headers["idempotency-key"]
        assert_equal({ "conversation_id" => "01AB", "content" => "hi",
                       "content_type" => "text/plain" }, JSON.parse(req.body))
        [201, envelope({})]
      end
      client.send_message("01AB", "hi", content_type: "text/plain", idempotency_key: "my-key")
    end

    def test_send_message_returns_message_with_reader_methods
      payload = { "id" => "01M1", "conversation_id" => "01AB", "sequence" => 1,
                  "content" => "hi", "content_type" => "text/markdown",
                  "created_at" => "2026-01-01T00:00:00Z",
                  "sender" => { "uuid" => "uuid-alice", "username" => "alice",
                                "nickname" => "Alice", "avatar_url" => nil } }
      client = with_server do |_req|
        [201, envelope(payload)]
      end

      message = client.send_message("01AB", "hi")
      assert_kind_of Message, message
      assert_kind_of Hash, message
      assert_equal "01M1", message.id
      assert_equal "01AB", message.conversation_id
      assert_equal "hi", message.content
      assert_equal "text/markdown", message.content_type
      assert_equal 1, message.sequence
      assert_equal "2026-01-01T00:00:00Z", message.created_at
      assert_equal "uuid-alice", message.sender.uuid
      assert_equal "alice", message.sender.username
      assert_equal "Alice", message.sender.nickname
      assert_nil message.sender.avatar_url
      assert_equal "hi", message["content"]
      assert_equal payload, JSON.parse(JSON.generate(message))
    end

    def test_messages_returns_message_objects
      client = with_server do |_req|
        [200, envelope([msg(1), msg(2)])]
      end
      list = client.messages("01AB")
      assert_equal %w[01M1 01M2], list.map(&:id)
      assert_equal [1, 2], list.map(&:sequence)
      assert_equal "alice", list.first.sender.username
    end

    def test_send_direct_message_creates_conversation_then_sends
      paths = []
      client = with_server do |req|
        paths << req.path
        case req.path
        when "/api/v1/conversations"
          assert_equal({ "kind" => "direct", "member_uuids" => ["uuid-bob"] }, JSON.parse(req.body))
          [201, envelope({ "id" => "01AB", "kind" => "direct" })]
        when "/api/v1/messages"
          assert_equal({ "conversation_id" => "01AB", "content" => "hi",
                         "content_type" => "text/markdown" }, JSON.parse(req.body))
          [201, envelope({ "id" => "01M1", "sequence" => 1 })]
        end
      end
      message = client.send_direct_message("uuid-bob", "hi")
      assert_equal "01M1", message["id"]
      assert_equal ["/api/v1/conversations", "/api/v1/messages"], paths
    end

    def test_send_direct_message_passes_options_to_send
      client = with_server do |req|
        if req.path == "/api/v1/messages"
          assert_equal "my-key", req.headers["idempotency-key"]
          assert_equal({ "conversation_id" => "01AB", "content" => "hi",
                         "content_type" => "text/plain" }, JSON.parse(req.body))
          [201, envelope({})]
        else
          [201, envelope({ "id" => "01AB", "kind" => "direct" })]
        end
      end
      client.send_direct_message("uuid-bob", "hi", content_type: "text/plain", idempotency_key: "my-key")
    end

    def test_auth_error_refreshes_credential_and_retries_once
      calls = 0
      client = with_server do |req|
        calls += 1
        if calls == 1
          assert_equal "Bearer #{CREDENTIAL}", req.headers["authorization"]
          [400, envelope(nil, code: 10_001, message: "Invalid bearer token")]
        else
          assert_equal "Bearer #{FRESH_CREDENTIAL}", req.headers["authorization"]
          [200, envelope({ "id" => 1 })]
        end
      end
      client.get_credential = -> { FRESH_CREDENTIAL }

      assert_equal({ "id" => 1 }, client.me)
      assert_equal 2, calls
      assert_equal FRESH_CREDENTIAL, client.credential
    end

    def test_no_refresh_without_callback
      client = with_server do |_req|
        [400, envelope(nil, code: 10_001, message: "Invalid bearer token")]
      end
      assert_raises(Error) { client.me }
      assert_equal 1, @server.requests.size
    end

    def test_no_refresh_when_callback_returns_same_credential
      client = with_server do |_req|
        [400, envelope(nil, code: 10_001, message: "Invalid bearer token")]
      end
      client.get_credential = -> { CREDENTIAL }
      assert_raises(Error) { client.me }
      assert_equal 1, @server.requests.size
    end

    def test_maps_envelope_errors_with_status
      client = with_server do |_req|
        [403, envelope(nil, code: 10_005, message: "Permission denied")]
      end
      error = assert_raises(Error) { client.list_groups }
      assert_equal 10_005, error.code
      assert_equal 403, error.status
      assert_equal "Permission denied", error.message
      refute error.auth_error?
    end

    def test_non_envelope_body_maps_to_code_minus_one
      client = with_server do |_req|
        [500, JSON.generate({ error: "boom" })]
      end
      error = assert_raises(Error) { client.me }
      assert_equal(-1, error.code)
      assert_equal 500, error.status
      refute_predicate error, :auth_error?
    end

    def test_transport_failure_maps_to_status_zero
      client = with_server do |_req|
        raise TestHTTPServer::Abort
      end
      error = assert_raises(Error) { client.me }
      assert_equal(-1, error.code)
      assert_equal 0, error.status
    end

    def test_storage_url_escapes_segments
      client = with_server { |req| [200, envelope(nil)] }
      assert_equal "#{@server.url}/api/v1/storage/attachments/202607/a%20b.png",
                   client.storage_url("attachments/202607/a b.png")
    end

    def test_upload_file_sends_multipart_and_parses_result
      client = with_server do |req|
        assert_equal "POST", req.method
        assert_equal "/api/v1/storage", req.path
        content_type = req.headers["content-type"]
        assert content_type.start_with?("multipart/form-data; boundary=")
        boundary = content_type[/boundary=(.+)\z/, 1]
        body = req.body
        assert_includes body, "--#{boundary}"
        assert_includes body, "name=\"file\"; filename=\"hello.txt\""
        assert_includes body, "Content-Type: text/plain"
        assert_includes body, "hello upload"
        assert_includes body, "name=\"dir\""
        assert_includes body, "attachments"
        [200, JSON.generate({ key: "a/1.txt", url: "http://x/1.txt", size: 12 })]
      end

      Tempfile.open("upload-fixture") do |tmp|
        tmp.write("hello upload")
        tmp.flush
        result = client.upload_file(tmp.path, filename: "hello.txt", dir: "attachments")
        assert_equal({ "key" => "a/1.txt", "url" => "http://x/1.txt", "size" => 12 }, result)
      end
    end

    def test_upload_file_accepts_io_with_explicit_filename
      client = with_server do |req|
        assert_includes req.body, "name=\"file\"; filename=\"report.pdf\""
        assert_includes req.body, "Content-Type: application/pdf"
        [200, JSON.generate({ key: "k", url: "", size: 4 })]
      end
      result = client.upload_file(StringIO.new("%PDF"), filename: "report.pdf")
      assert_equal "k", result["key"]
    end

    def test_upload_error_raises
      client = with_server do |_req|
        [400, JSON.generate({ error: "no file" })]
      end
      error = assert_raises(Error) { client.upload_file(StringIO.new("x"), filename: "x.txt") }
      assert_equal(-1, error.code)
      assert_equal "no file", error.message
    end

    private

    def msg(sequence)
      { "id" => "01M#{sequence}", "conversation_id" => "01AB",
        "sender" => { "uuid" => "uuid-alice", "username" => "alice",
                      "nickname" => "Alice", "avatar_url" => nil },
        "content" => "m#{sequence}", "content_type" => "text/plain",
        "created_at" => "2026-01-01T00:00:00Z", "sequence" => sequence }
    end
  end
end
