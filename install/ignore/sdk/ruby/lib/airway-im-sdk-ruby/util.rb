# frozen_string_literal: true

module AirwayIM
  module Util # :nodoc:
    # RFC 3986 unreserved characters — everything else is percent-encoded.
    UNRESERVED = /[^A-Za-z0-9\-._~]/.freeze

    module_function

    # Identifiers are opaque strings; escape them so "/" or "?" in a value
    # can never change the request path. Path-segment escaping: a space
    # becomes %20 (form encoding's "+" would change the value in a path).
    def escape_segment(value)
      value.to_s.gsub(UNRESERVED) { |char| char.bytes.map { |byte| format("%%%02X", byte) }.join }
    end
  end
end
