# frozen_string_literal: true

require "securerandom"

module AirwayIM
  # REST client for the Airway IM plugin public API (default port 1905).
  # Authenticates every request with a credential issued by the host
  # backend — this gem never issues credentials to end users.
  #
  #   im = AirwayIM::Client.new(
  #     api_url: "https://im.example.com",
  #     credential: host_issued_credential,
  #     get_credential: -> { fetch_fresh_credential },  # optional, on 401/10001
  #   )
  #
  # All methods return the envelope's `data` (a Hash or Array) and raise
  # AirwayIM::Error on failure.
  class Client
    DEFAULT_CONTENT_TYPE = "text/markdown"

    MIME_TYPES = {
      ".png" => "image/png", ".jpg" => "image/jpeg", ".jpeg" => "image/jpeg",
      ".gif" => "image/gif", ".webp" => "image/webp", ".svg" => "image/svg+xml",
      ".pdf" => "application/pdf", ".txt" => "text/plain", ".md" => "text/markdown",
      ".mp4" => "video/mp4", ".mov" => "video/quicktime", ".mp3" => "audio/mpeg",
      ".zip" => "application/zip", ".json" => "application/json"
    }.freeze

    # @return [String] current credential; replaced after a successful refresh
    attr_accessor :credential

    # Optional refresh callback; may also be reassigned after construction.
    attr_accessor :get_credential

    def initialize(api_url:, credential:, get_credential: nil, timeout: 15)
      @http = HTTP.new(api_url, timeout: timeout)
      @credential = credential
      @get_credential = get_credential
    end

    # ---- Identity ----

    def me
      get("/api/v1/me")
    end

    # ---- Conversations ----

    # List my active group conversations (direct ones are excluded by the
    # backend).
    def list_groups
      get("/api/v1/conversations", query: { type: "group" })
    end

    # Create or resolve a conversation. kind: "direct" (get-or-create, may
    # return an existing conversation) or "group" (always creates). member_uuids
    # lists the *other* users by their stable identity uuid; the authenticated
    # user must not be included.
    def create_conversation(kind:, member_uuids:, title: nil)
      body = { kind: kind, member_uuids: member_uuids }
      body[:title] = title unless title.nil?
      post("/api/v1/conversations", body: body)
    end

    # Get-or-create the direct conversation with one other user, identified
    # by their uuid.
    def create_direct(other_uuid)
      create_conversation(kind: "direct", member_uuids: [other_uuid])
    end

    # The direct conversation with one other user, identified by their uuid.
    # Read-only: returns nil when none exists yet (create_direct get-or-creates
    # instead). Combine with messages/each_message to poll and display the
    # history with that user.
    def direct_conversation(other_uuid)
      get("/api/v1/conversations/direct/#{Util.escape_segment(other_uuid)}")
    rescue Error => e
      raise unless e.code == 11001

      nil
    end

    # Create a new group; the authenticated user becomes its owner.
    def create_group(member_uuids:, title: nil)
      body = { member_uuids: member_uuids }
      body[:title] = title unless title.nil?
      post("/api/v1/group", body: body)
    end

    # Conversation kind plus active members with roles.
    def conversation(uuid)
      get("/api/v1/conversations/#{Util.escape_segment(uuid)}")
    end

    # Add members (identified by uuid) to a group (owner/admin). Idempotent
    # for already-active members.
    def add_members(uuid, member_uuids)
      post("/api/v1/conversations/#{Util.escape_segment(uuid)}/members",
           body: { member_uuids: member_uuids })
    end

    # Remove one member (identified by uuid) from a group (owner/admin; cannot
    # remove self or the owner). Idempotent when the user is not an active
    # member.
    def remove_member(uuid, user_uuid)
      delete("/api/v1/conversations/#{Util.escape_segment(uuid)}/members/#{Util.escape_segment(user_uuid)}")
    end

    # ---- Messages ----

    # Ordered message page after a sequence; use for history display and
    # reconnect synchronization. limit: 1-200, the backend falls back to 100
    # outside that range.
    def messages(uuid, after_sequence: nil, limit: nil)
      query = { after_sequence: after_sequence, limit: limit }
      get("/api/v1/conversations/#{Util.escape_segment(uuid)}/messages", query: query)
    end

    # Auto-paging enumerator over the conversation history in ascending
    # sequence order; stops when a page comes back short or empty.
    #
    #   im.each_message(uuid, after_sequence: 42).each { |msg| ... }
    def each_message(uuid, after_sequence: 0, page_size: 100, &block)
      page_size = 200 if page_size > 200
      return enum_for(:each_message, uuid, after_sequence: after_sequence, page_size: page_size) unless block

      cursor = after_sequence
      loop do
        page = messages(uuid, after_sequence: cursor, limit: page_size)
        break if page.empty?

        page.each { |message| yield message }
        cursor = page.last["sequence"]
        break if page.size < page_size
      end
    end

    # Send a message by conversation id. A random Idempotency-Key is
    # generated per call and reused across network-failure retries, so retry
    # storms can never duplicate a message; pass idempotency_key to control
    # it (1-128 chars, reuse only for the same logical request).
    def send_message(conversation_id, content, content_type: DEFAULT_CONTENT_TYPE, idempotency_key: nil, retries: 1)
      body = { conversation_id: conversation_id, content: content, content_type: content_type }
      post_message("/api/v1/messages", body, idempotency_key: idempotency_key, retries: retries)
    end

    # Send a direct message to one other user, identified by their uuid:
    # get-or-create the direct conversation, then send. Same idempotency
    # semantics as send_message.
    def send_direct_message(other_uuid, content, content_type: DEFAULT_CONTENT_TYPE, idempotency_key: nil, retries: 1)
      conversation = create_direct(other_uuid)
      send_message(conversation.fetch("id"), content,
                   content_type: content_type, idempotency_key: idempotency_key, retries: retries)
    end

    # ---- Storage ----

    # Upload a file (development-stage API: currently no auth middleware).
    # Accepts a filesystem path or any IO-like object; the whole file is
    # read into memory. Returns {"key" => ..., "url" => ..., "size" => ...}.
    def upload_file(path_or_io, filename: nil, dir: nil)
      headers, _, body = upload_parts(path_or_io, filename, dir)
      status, payload = @http.transport(:post, "/api/v1/storage", headers, nil, body)
      if (200...300).cover?(status) && payload.is_a?(Hash) && payload["key"]
        return payload
      end

      message = payload.is_a?(Hash) ? payload["error"] : nil
      raise Error.new(-1, message || "HTTP #{status}: upload failed", status)
    end

    # Public download URL for a storage key (no auth, like the API itself).
    def storage_url(key)
      escaped = key.to_s.split("/").map { |segment| Util.escape_segment(segment) }.join("/")
      "#{@http.base_url}/api/v1/storage/#{escaped}"
    end

    private

    def get(path, query: nil)
      request(:get, path, query: query)
    end

    def post(path, body:)
      request(:post, path, body: body)
    end

    def delete(path)
      request(:delete, path)
    end

    def post_message(path, body, idempotency_key:, retries:)
      key = idempotency_key || SecureRandom.uuid
      attempts = 0
      begin
        request(:post, path, headers: { "Idempotency-Key" => key }, body: body)
      rescue Error => e
        attempts += 1
        # Retry transport failures (status 0) only; HTTP errors are final and
        # the backend dedupes by the reused idempotency key anyway.
        raise if e.status != 0 || attempts > retries

        retry
      end
    end

    def request(method, path, headers: {}, query: nil, body: nil)
      auth_error = nil
      begin
        return @http.request(method, path,
                             headers: { "Authorization" => "Bearer #{@credential}" }.merge(headers),
                             query: query, body: body)
      rescue Error => e
        # Credential expired/revoked: refresh once through the host callback
        # and retry a single time. Other errors propagate unchanged.
        raise unless refreshable?(e)

        auth_error = e
      end

      fresh = @get_credential.call
      raise auth_error if fresh.nil? || fresh.empty? || fresh == @credential

      @credential = fresh
      @http.request(method, path,
                    headers: { "Authorization" => "Bearer #{@credential}" }.merge(headers),
                    query: query, body: body)
    end

    def refreshable?(error)
      return false unless error.auth_error?
      return false if @get_credential.nil?
      return false if @credential.nil? || @credential.empty?

      true
    end

    # Builds the multipart/form-data parts for upload_file:
    # [headers, query, body] to splat into HTTP#transport.
    def upload_parts(path_or_io, filename, dir)
      content, filename = read_upload(path_or_io, filename)
      boundary = "AirwayIM#{SecureRandom.hex(16)}"
      body = multipart_body(content, filename, dir, boundary)
      [{ "Content-Type" => "multipart/form-data; boundary=#{boundary}" }, nil, body]
    end

    def read_upload(path_or_io, filename)
      if path_or_io.respond_to?(:read)
        io = path_or_io
        name = filename || (io.respond_to?(:path) && io.path ? File.basename(io.path) : nil) || "upload"
        [io.read, name]
      else
        path = path_or_io.to_s
        [File.binread(path), filename || File.basename(path)]
      end
    end

    def multipart_body(content, filename, dir, boundary)
      disposition = "Content-Disposition: form-data; name=\"file\"; filename=\"#{sanitize_filename(filename)}\""
      mime = mime_type(filename)
      body = String.new(encoding: Encoding::BINARY)
      body << "--#{boundary}\r\n#{disposition}\r\nContent-Type: #{mime}\r\n\r\n"
      body << content.to_s.dup.force_encoding(Encoding::BINARY)
      body << "\r\n"
      if dir
        body << "--#{boundary}\r\nContent-Disposition: form-data; name=\"dir\"\r\n\r\n#{dir}\r\n"
      end
      body << "--#{boundary}--\r\n"
      body
    end

    def sanitize_filename(filename)
      filename.to_s.gsub(/[\r\n"\\]/, "_")
    end

    def mime_type(filename)
      MIME_TYPES.fetch(File.extname(filename.to_s).downcase, "application/octet-stream")
    end
  end
end
