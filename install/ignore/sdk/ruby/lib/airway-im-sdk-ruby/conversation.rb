# frozen_string_literal: true

module AirwayIM
  # Conversation object: one object per conversation carrying its id
  # and kind, mirroring the TS SDK's Conversation minus the realtime
  # layer (the server-side SDKs poll the sequence-based sync API instead of
  # subscribing to gateway events).
  #
  #   direct = im.create_direct("user-2")
  #   direct.send_message("hi")
  #   direct.each_message.each { |msg| ... }
  class Conversation
    attr_reader :id, :kind

    def initialize(client, id, kind)
      @client = client
      @id = id
      @kind = kind
    end

    # Send a message to this conversation (idempotency semantics as
    # Client#send_message).
    def send_message(content, content_type: Client::DEFAULT_CONTENT_TYPE, idempotency_key: nil, retries: 1)
      @client.send_message(@id, content,
                           content_type: content_type,
                           idempotency_key: idempotency_key, retries: retries)
    end

    # Ordered message page in this conversation (list_messages semantics).
    def list_messages(after_sequence: nil, limit: nil)
      @client.list_messages(@id, after_sequence: after_sequence, limit: limit)
    end

    # Auto-paging enumerator over this conversation's history (each_message
    # semantics).
    def each_message(after_sequence: 0, page_size: 100, &block)
      @client.each_message(@id, after_sequence: after_sequence, page_size: page_size, &block)
    end

    # Conversation kind plus members with roles, fresh from the API.
    def details
      @client.get_conversation(@id)
    end
  end

  # A direct (1:1) conversation; the peer of an incoming message is
  # message.sender.
  class DirectConversation < Conversation
    def initialize(client, id)
      super(client, id, "direct")
    end
  end

  # A group conversation; carries the group-only operations. title is the
  # creation-time title (nil when created without one or when opened by id);
  # details returns the live value.
  class GroupConversation < Conversation
    attr_reader :title

    def initialize(client, id, title = nil)
      super(client, id, "group")
      @title = title
    end

    # Add members by uuid (owner/admin; idempotent for already-active members).
    def add_members(member_uuids)
      @client.add_members(@id, member_uuids)
    end

    # Remove members by uuid (owner/admin; cannot remove self or the owner).
    def remove_members(user_uuids)
      @client.remove_members(@id, user_uuids)
    end
  end
end
