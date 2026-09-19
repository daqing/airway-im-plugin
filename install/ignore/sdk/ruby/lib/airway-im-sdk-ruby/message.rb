# frozen_string_literal: true

module AirwayIM
  # A message response that behaves like a Hash (JSON serialization,
  # equality, [] access) while also exposing its fields as reader methods:
  # message.id, message.sequence, message.sender.uuid, and so on. A Hash
  # subclass on purpose so both access styles stay equivalent.
  class Message < Hash
    FIELDS = %w[id conversation_id content content_type created_at sequence].freeze

    def initialize(data = {})
      super()
      update(data || {})
    end

    FIELDS.each do |field|
      define_method(field) { self[field] }
    end

    def sender
      Sender.new(self["sender"] || {})
    end
  end

  # Author profile inside a Message, same Hash-with-readers contract:
  # message.sender.uuid or message["sender"]["uuid"].
  class Sender < Hash
    FIELDS = %w[uuid username nickname avatar_url].freeze

    def initialize(data = {})
      super()
      update(data || {})
    end

    FIELDS.each do |field|
      define_method(field) { self[field] }
    end
  end
end
