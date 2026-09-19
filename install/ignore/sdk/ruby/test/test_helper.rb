# frozen_string_literal: true

require "json"
require "minitest/autorun"
require "socket"

require_relative "../lib/airway-im-sdk-ruby"

# Minimal HTTP/1.1 stub server backed by TCPServer — stdlib only, just
# enough for Net::HTTP to exercise real request/response I/O. One response
# per connection (Connection: close), one handler per test.
class TestHTTPServer
  # Raised by a handler to simulate a transport failure: the connection is
  # closed without any response, so the client sees a network error.
  class Abort < StandardError; end

  Request = Struct.new(:method, :path, :headers, :body, keyword_init: true)

  attr_reader :port, :requests
  # First exception raised by a handler (e.g. a failing assertion) — the
  # client only sees a closed connection, so stop() re-raises it.
  attr_accessor :handler_error

  # handler: Request -> [status Integer, body String]
  def initialize(&handler)
    @handler = handler
    @requests = []
    @server = TCPServer.new("127.0.0.1", 0)
    @port = @server.addr[1]
    @thread = Thread.new { accept_loop }
  end

  def stop
    @thread&.kill
    @server.close
    raise handler_error if handler_error
  end

  def url
    "http://127.0.0.1:#{@port}"
  end

  private

  def accept_loop
    loop do
      client = @server.accept
      Thread.new(client) { |socket| serve(socket) }
    end
  end

  def serve(socket)
    request = read_request(socket)
    return if request.nil?

    @requests << request
    status, body = @handler.call(request)
    payload = +"HTTP/1.1 #{status} #{reason(status)}\r\n"
    payload << "Content-Type: application/json\r\n"
    payload << "Content-Length: #{body.bytesize}\r\n"
    payload << "Connection: close\r\n\r\n"
    payload << body
    socket.write(payload)
  rescue StandardError => e
    # Handler failures (including Abort) close the connection without a
    # response so the client observes a transport-level error. Remember the
    # first one so stop() can surface it (matters for handler assertions).
    @handler_error ||= e unless e.is_a?(TestHTTPServer::Abort)
    nil
  ensure
    begin
      socket.close unless socket.closed?
    rescue StandardError
      nil
    end
  end

  def read_request(socket)
    request_line = socket.gets("\r\n")
    return nil if request_line.nil?

    method, path = request_line.split
    headers = {}
    while (line = socket.gets("\r\n")) && line != "\r\n"
      name, value = line.split(":", 2)
      headers[name.strip.downcase] = value.strip
    end
    body = headers["content-length"] ? socket.read(headers["content-length"].to_i) : ""
    Request.new(method: method, path: path, headers: headers, body: body)
  end

  def reason(status)
    {
      200 => "OK", 201 => "Created", 400 => "Bad Request", 401 => "Unauthorized",
      403 => "Forbidden", 404 => "Not Found", 409 => "Conflict", 500 => "Internal Server Error"
    }.fetch(status, "Status")
  end
end

def envelope(data, code: 0, message: nil)
  JSON.generate({ code: code, data: data, message: message })
end
