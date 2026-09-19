# frozen_string_literal: true

module AirwayIM
  # Business codes from the backend's {code, data, message} envelope
  # (install/deps/im/docs/api/openapi.md).
  module ErrorCode
    INTERNAL = 10_000
    INVALID_CREDENTIAL = 10_001
    INVALID_REQUEST = 10_003
    PERMISSION_DENIED = 10_005
    CONVERSATION_NOT_FOUND = 11_001
    IDEMPOTENCY_KEY_REUSED = 11_002
  end

  class Error < StandardError
    # Envelope business code (10000-11002); -1 when the body was not an
    # envelope or the failure happened before a response arrived.
    attr_reader :code

    # HTTP status code; 0 for transport failures (network error, timeout).
    attr_reader :status

    def initialize(code, message, status)
      super(message)
      @code = code
      @status = status
    end

    # The credential is missing, malformed, revoked, or expired.
    def auth_error?
      @code == ErrorCode::INVALID_CREDENTIAL || @status == 401
    end
  end
end
