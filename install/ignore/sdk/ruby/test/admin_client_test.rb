# frozen_string_literal: true

require "test_helper"

module AirwayIM
  class AdminClientTest < Minitest::Test
    USERNAME = "admin"
    PASSWORD = "secret"

    def teardown
      @server&.stop
    end

    def with_server(&handler)
      @server = TestHTTPServer.new(&handler)
      AdminClient.new(api_url: @server.url, username: USERNAME, password: PASSWORD)
    end

    def login_ok
      [200, envelope({ token: "session-token", expires_at: "2026-09-20T08:00:00Z",
                       username: USERNAME })]
    end

    def test_login_returns_session_payload
      client = with_server do |req|
        assert_equal "POST", req.method
        assert_equal "/admin/api/login", req.path
        assert_equal({ "username" => USERNAME, "password" => PASSWORD }, JSON.parse(req.body))
        login_ok
      end
      data = client.login
      assert_equal "session-token", data["token"]
    end

    def test_first_authenticated_call_logs_in_implicitly
      client = with_server do |req|
        case req.path
        when "/admin/api/login"
          login_ok
        when "/admin/api/users"
          assert_equal "GET", req.method
          assert_equal "Bearer session-token", req.headers["authorization"]
          [200, envelope([{ "id" => 1, "uuid" => "user-42", "token_version" => 1 }])]
        else
          [404, envelope(nil, code: -2, message: "unexpected")]
        end
      end
      users = client.users
      assert_equal 1, users.size
      assert_equal "/admin/api/login", @server.requests[0].path
      assert_equal "/admin/api/users", @server.requests[1].path
    end

    def test_401_triggers_single_relogin_and_retry
      logins = 0
      user_calls = 0
      client = with_server do |req|
        case req.path
        when "/admin/api/login"
          logins += 1
          login_ok
        when "/admin/api/users"
          user_calls += 1
          if user_calls == 1
            [401, envelope(nil, code: 20_001, message: "Administrator authentication required")]
          else
            [200, envelope([])]
          end
        end
      end
      assert_equal [], client.users
      assert_equal 2, logins
      assert_equal 2, user_calls
    end

    def test_relogin_failure_propagates
      client = with_server do |req|
        case req.path
        when "/admin/api/login"
          login_ok
        when "/admin/api/users"
          [401, envelope(nil, code: 20_001, message: "Administrator authentication required")]
        end
      end
      error = assert_raises(Error) { client.users }
      assert_equal 401, error.status
      # login, users(401), re-login, users(401 again)
      assert_equal 4, @server.requests.size
    end

    def test_admin_operations_target_the_right_paths
      client = with_server do |req|
        case req.path
        when "/admin/api/login" then login_ok
        when "/admin/api/users/user-42/revoke"
          assert_equal "POST", req.method
          [200, envelope({ uuid: "user-42", token_version: 2, connections_kicked: 1 })]
        when "/admin/api/status"
          [200, envelope({ users: { total: 1 } })]
        when "/admin/api/conversations"
          [200, envelope([])]
        when "/admin/api/conversations/01AB/messages"
          [200, envelope([])]
        when "/admin/api/messages/01M1/mark-illegal"
          assert_equal "POST", req.method
          [200, envelope({ id: "01M1", is_illegal: true })]
        end
      end
      client.revoke_user("user-42")
      client.status
      client.group_conversations
      client.conversation_messages("01AB")
      client.mark_illegal("01M1")
      assert_equal 6, @server.requests.size
    end

    def test_logout_clears_session_and_next_call_logs_in_again
      client = with_server do |req|
        case req.path
        when "/admin/api/login" then login_ok
        when "/admin/api/logout"
          assert_equal "Bearer session-token", req.headers["authorization"]
          [200, envelope(nil)]
        when "/admin/api/users"
          assert_equal "Bearer session-token", req.headers["authorization"]
          [200, envelope([])]
        end
      end
      client.logout
      client.users
      assert_equal "/admin/api/login", @server.requests[2].path
      assert_equal "/admin/api/users", @server.requests[3].path
    end
  end
end
