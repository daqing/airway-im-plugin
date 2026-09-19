# frozen_string_literal: true

# airway-im-sdk-ruby — server-side Ruby SDK for the Airway IM plugin
# (https://github.com/daqing/airway-im-plugin).
#
# Quick start:
#
#   require "airway-im-sdk-ruby"
#
#   # Mint a credential for a logged-in user (server-to-server, internal API)
#   internal = AirwayIM::InternalClient.new(
#     internal_url: "http://127.0.0.1:1906",
#     internal_secret: ENV["IM_INTERNAL_SECRET"],
#   )
#   minted = internal.mint_credential(uuid: "user-42", name: "alice")
#
#   # Call the IM API on the user's behalf
#   im = AirwayIM::Client.new(api_url: "https://im.example.com", credential: minted.credential)
#   im.me
#   conversation = im.create_direct(2)
#   im.send_message(conversation["id"], "Hello **team**!")

require_relative "airway-im-sdk-ruby/version"
require_relative "airway-im-sdk-ruby/error"
require_relative "airway-im-sdk-ruby/util"
require_relative "airway-im-sdk-ruby/credentials"
require_relative "airway-im-sdk-ruby/http"
require_relative "airway-im-sdk-ruby/client"
require_relative "airway-im-sdk-ruby/internal_client"
require_relative "airway-im-sdk-ruby/admin_client"
