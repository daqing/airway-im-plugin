# frozen_string_literal: true

require "test_helper"

module AirwayIM
  class CredentialsTest < Minitest::Test
    SECRET = "test-secret"

    # Cross-language regression vector generated with the Node.js reference
    # implementation from install/deps/im/docs/design/identity.md §4.2:
    # claims {uuid: "user-42", name: "alice", iat: 1700000000, exp: 1700086400}.
    NODE_VECTOR = "im1.eyJ1dWlkIjoidXNlci00MiIsIm5hbWUiOiJhbGljZSIsImlhdCI6MTcwMDAwMDAwMCwiZXhwIjoxNzAwMDg2NDAwfQ.w_i4UIKCVjBY0XwnDdeRsnOCHoqgxxl1rDAESDh7VeU"

    def test_sign_matches_node_reference_vector_byte_for_byte
      credential = Credentials.sign(
        secret: SECRET,
        uuid: "user-42",
        name: "alice",
        now: Time.at(1_700_000_000),
        ttl: 86_400
      )
      assert_equal NODE_VECTOR, credential
    end

    def test_sign_includes_optional_fields_when_present
      credential = Credentials.sign(
        secret: SECRET,
        uuid: "u1",
        name: "bob",
        nickname: "Bob",
        avatar_url: "https://example.com/bob.png",
        token_version: 3,
        now: Time.at(1_700_000_000),
        ttl: 60
      )
      claims = Credentials.decode(credential)
      assert_equal({ "uuid" => "u1", "name" => "bob", "nickname" => "Bob",
                     "avatar_url" => "https://example.com/bob.png",
                     "token_version" => 3, "iat" => 1_700_000_000,
                     "exp" => 1_700_000_060 }, claims)
    end

    def test_sign_omits_nil_optional_fields
      credential = Credentials.sign(secret: SECRET, uuid: "u1", name: "bob", now: Time.at(0))
      claims = Credentials.decode(credential)
      assert_equal({ "uuid" => "u1", "name" => "bob", "iat" => 0, "exp" => 86_400 }, claims)
    end

    def test_sign_omits_exp_when_ttl_is_nil
      credential = Credentials.sign(secret: SECRET, uuid: "u1", name: "bob", ttl: nil, now: Time.at(0))
      claims = Credentials.decode(credential)
      assert_nil claims["exp"]
      assert Credentials.verify_signature?(credential, SECRET)
    end

    def test_sign_rejects_out_of_range_fields
      assert_raises(ArgumentError) do
        Credentials.sign(secret: SECRET, uuid: "x" * 65, name: "bob")
      end
      assert_raises(ArgumentError) do
        Credentials.sign(secret: SECRET, uuid: "u1", name: "")
      end
      assert_raises(ArgumentError) do
        Credentials.sign(secret: SECRET, uuid: "u1", name: "bob", nickname: "x" * 65)
      end
      assert_raises(ArgumentError) do
        Credentials.sign(secret: SECRET, uuid: "u1", name: "bob", avatar_url: "x" * 2049)
      end
      assert_raises(ArgumentError) do
        Credentials.sign(secret: SECRET, uuid: "u1", name: "bob", token_version: 0)
      end
    end

    def test_decode_round_trip
      claims = { "uuid" => "user-42", "name" => "alice",
                 "iat" => 1_700_000_000, "exp" => 1_700_086_400 }
      assert_equal claims, Credentials.decode(NODE_VECTOR)
    end

    def test_decode_rejects_malformed_input
      ["", "not-a-credential", "im2.aaa.bbb", "im1.aaa", "im1.aaa.bbb.ccc"].each do |bad|
        assert_raises(Error) { Credentials.decode(bad) }
      end
    end

    def test_verify_signature_accepts_valid_and_rejects_tampered
      assert Credentials.verify_signature?(NODE_VECTOR, SECRET)
      refute Credentials.verify_signature?(NODE_VECTOR, "wrong-secret")

      parts = NODE_VECTOR.split(".")
      parts[1] = (parts[1][0] == "A" ? "B" : "A") + parts[1][1..]
      refute Credentials.verify_signature?(parts.join("."), SECRET)
      refute Credentials.verify_signature?("", SECRET)
      refute Credentials.verify_signature?("garbage", SECRET)
    end

    def test_expired
      refute Credentials.expired?(NODE_VECTOR, now: Time.at(1_700_086_399))
      assert Credentials.expired?(NODE_VECTOR, now: Time.at(1_700_086_400))

      never = Credentials.sign(secret: SECRET, uuid: "u1", name: "bob", ttl: nil, now: Time.at(0))
      refute Credentials.expired?(never, now: Time.at(4_102_444_800))
    end
  end
end
