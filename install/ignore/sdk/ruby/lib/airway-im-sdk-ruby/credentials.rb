# frozen_string_literal: true

require "base64"
require "json"
require "openssl"

module AirwayIM
  # Airway-signed HMAC credentials
  # (install/deps/im/docs/design/identity.md):
  #
  #   im1.<base64url(payload JSON)>.<base64url(HMAC-SHA256(secret, "im1." + payload))>
  #
  # Use Credentials.sign when your backend is trusted with IM_AUTH_SECRET
  # (Option A); use AirwayIM::InternalClient#mint_credential to have the IM
  # backend mint revocable credentials instead (Option B).
  module Credentials
    PREFIX = "im1."
    DEFAULT_TTL = 86_400

    module_function

    # Sign (uuid, name) with the shared IM_AUTH_SECRET. Optional display
    # fields are write-only-when-present: passing nil never clobbers a
    # richer profile stored earlier in the IM users table. ttl: seconds
    # until exp; nil omits the claim entirely (service credentials only).
    # now: injectable clock, mainly for tests.
    def sign(secret:, uuid:, name:, nickname: nil, avatar_url: nil, token_version: nil, ttl: DEFAULT_TTL, now: Time.now)
      validate_length(uuid, 1, 64, "uuid")
      validate_length(name, 1, 64, "name")
      validate_length(nickname, 1, 64, "nickname") if nickname
      validate_length(avatar_url, 1, 2048, "avatar_url") if avatar_url
      if token_version && !(token_version.is_a?(Integer) && token_version >= 1)
        raise ArgumentError, "token_version must be an integer >= 1"
      end

      claims = { "uuid" => uuid, "name" => name }
      claims["nickname"] = nickname if nickname
      claims["avatar_url"] = avatar_url if avatar_url
      claims["token_version"] = token_version if token_version
      issued_at = now.to_i
      claims["iat"] = issued_at
      claims["exp"] = issued_at + ttl if ttl

      payload = Base64.urlsafe_encode64(JSON.generate(claims), padding: false)
      signature = Base64.urlsafe_encode64(
        OpenSSL::HMAC.digest("sha256", secret, "#{PREFIX}#{payload}"),
        padding: false
      )
      "#{PREFIX}#{payload}.#{signature}"
    end

    # Decode the claims segment. Raises Error (code -1) on malformed input.
    def decode(credential)
      payload = segments(credential)[1]
      JSON.parse(Base64.urlsafe_decode64(payload))
    rescue ArgumentError, JSON::ParserError
      raise Error.new(-1, "malformed credential: invalid payload", 0)
    end

    # Recompute the HMAC over the received payload and compare in constant
    # time. Never raises on malformed input.
    def verify_signature?(credential, secret)
      prefix, payload, signature = segments(credential)
      expected = OpenSSL::HMAC.digest("sha256", secret, "#{prefix}.#{payload}")
      OpenSSL.secure_compare(expected, Base64.urlsafe_decode64(signature))
    rescue ArgumentError, Error
      false
    end

    # exp is a unix-seconds claim; a credential without exp never expires
    # here (the backend treats it the same way).
    def expired?(credential_or_claims, now: Time.now)
      claims = credential_or_claims.is_a?(Hash) ? credential_or_claims : decode(credential_or_claims)
      exp = claims["exp"]
      !exp.nil? && exp <= now.to_i
    end

    def validate_length(value, min, max, field)
      return if value.to_s.length.between?(min, max)

      raise ArgumentError, "#{field} must be #{min}-#{max} characters"
    end

    def segments(credential)
      parts = credential.to_s.split(".")
      unless parts.length == 3 && parts[0] == "im1"
        raise Error.new(-1, "malformed credential: expected im1.<payload>.<signature>", 0)
      end

      parts
    end
  end
end
