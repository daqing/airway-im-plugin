# frozen_string_literal: true

require_relative "lib/airway-im-sdk-ruby/version"

Gem::Specification.new do |spec|
  spec.name = "airway-im-sdk-ruby"
  spec.version = AirwayIM::VERSION
  spec.authors = ["David Zhang"]
  spec.email = ["daqing@mzevo.com"]

  spec.summary = "Airway IM SDK for Ruby"
  spec.description = "Server-side Ruby SDK for the Airway IM plugin: credential " \
                     "signing and minting, a REST client for conversations, " \
                     "messages and storage, and an admin API client. Zero runtime " \
                     "dependencies (standard library only)."
  spec.homepage = "https://github.com/daqing/airway-im-plugin"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.1"

  spec.metadata["homepage_uri"] = spec.homepage
  spec.metadata["source_code_uri"] = "#{spec.homepage}/tree/main/install/ignore/sdk/ruby"
  spec.metadata["documentation_uri"] = "#{spec.homepage}/blob/main/install/ignore/sdk/ruby/README.md"

  spec.files = Dir["lib/**/*.rb", "README.md"]
  spec.require_paths = ["lib"]
end
