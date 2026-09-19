# frozen_string_literal: true

require "json"
require "net/http"
require "openssl"
require "uri"

module AirwayIM
  # Internal transport shared by all clients: a Net::HTTP wrapper plus
  # {code, data, message} envelope unwrapping
  # (install/deps/im/docs/api/openapi.md).
  #
  # A fresh Net::HTTP connection is opened per request; that keeps instances
  # safe to share across threads (Puma-style servers) at the cost of one
  # TCP handshake per call, which server-side integrations can afford.
  class HTTP # :nodoc:
    TRANSPORT_ERRORS = [
      EOFError, IOError, SocketError, SystemCallError,
      OpenSSL::SSL::SSLError,
      Net::OpenTimeout, Net::ReadTimeout, Net::WriteTimeout,
      Net::HTTPBadResponse
    ].freeze

    attr_reader :base_url

    def initialize(base_url, timeout:)
      @base_url = base_url.to_s.sub(%r{/+\z}, "")
      @timeout = timeout
    end

    # Perform the request and unwrap the envelope: returns the envelope's
    # data on success, raises Error otherwise.
    def request(method, path, headers: {}, query: nil, body: nil)
      status, payload = transport(method, path, headers, query, body)
      envelope = payload.is_a?(Hash) && payload.key?("code") ? payload : nil

      if (200...300).cover?(status) && envelope.is_a?(Hash) && envelope["code"] == 0
        return envelope["data"]
      end
      if envelope.is_a?(Hash) && envelope["code"].is_a?(Integer)
        raise Error.new(envelope["code"], envelope["message"] || "HTTP #{status}", status)
      end
      raise Error.new(-1, "HTTP #{status}: unexpected response", status)
    end

    # Raw request for endpoints outside the envelope contract (storage).
    # Returns [status, parsed-json-or-string-or-nil].
    def transport(method, path, headers = {}, query = nil, body = nil)
      response = perform(build_uri(path, query), method, headers, body)
      [response.code.to_i, parse(response.body)]
    rescue *TRANSPORT_ERRORS => e
      raise Error.new(-1, e.message, 0)
    end

    private

    def build_uri(path, query)
      uri = URI("#{@base_url}#{path}")
      params = query.is_a?(Hash) ? query.compact : query
      uri.query = URI.encode_www_form(params) if params && !params.empty?
      uri
    end

    def perform(uri, method, headers, body)
      http = Net::HTTP.new(uri.host, uri.port)
      http.use_ssl = uri.scheme == "https"
      http.open_timeout = @timeout
      http.read_timeout = @timeout
      http.write_timeout = @timeout if http.respond_to?(:write_timeout=)

      request = Net::HTTP.const_get(request_class(method)).new(uri)
      headers.each { |name, value| request[name] = value }
      if body.is_a?(Hash) || body.is_a?(Array)
        request["Content-Type"] = "application/json"
        request.body = JSON.generate(body)
      else
        request.body = body
      end
      http.start { http.request(request) }
    end

    def request_class(method)
      case method
      when :get then "Get"
      when :post then "Post"
      when :delete then "Delete"
      when :put then "Put"
      else
        raise ArgumentError, "unsupported HTTP method: #{method.inspect}"
      end
    end

    def parse(body)
      return nil if body.nil? || body.empty?

      JSON.parse(body)
    rescue JSON::ParserError
      body
    end
  end
end
