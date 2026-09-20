<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Airway-signed HMAC credentials
 * (install/deps/im/docs/design/identity.md):
 *
 *   im1.<base64url(payload JSON)>.<base64url(HMAC-SHA256(secret, "im1." + payload))>
 *
 * Use Credentials::sign when your backend is trusted with IM_AUTH_SECRET
 * (Option A); use InternalClient::mintCredential to have the IM backend
 * mint revocable credentials instead (Option B).
 */
final class Credentials
{
    public const PREFIX = 'im1.';
    public const DEFAULT_TTL = 86400;

    /**
     * Sign (uuid, name) with the shared IM_AUTH_SECRET. Optional display
     * fields are write-only-when-present: passing null never clobbers a
     * richer profile stored earlier in the IM users table. $ttl: seconds
     * until exp; null omits the claim entirely (service credentials only).
     * $now: injectable unix-seconds clock, mainly for tests.
     */
    public static function sign(
        string $secret,
        string $uuid,
        string $name,
        ?string $nickname = null,
        ?string $avatarUrl = null,
        ?int $tokenVersion = null,
        ?int $ttl = self::DEFAULT_TTL,
        ?int $now = null
    ): string {
        self::validateLength($uuid, 1, 64, 'uuid');
        self::validateLength($name, 1, 64, 'name');
        if ($nickname !== null) {
            self::validateLength($nickname, 1, 64, 'nickname');
        }
        if ($avatarUrl !== null) {
            self::validateLength($avatarUrl, 1, 2048, 'avatar_url');
        }
        if ($tokenVersion !== null && $tokenVersion < 1) {
            throw new \InvalidArgumentException('token_version must be an integer >= 1');
        }

        $issuedAt = $now ?? time();
        $claims = ['uuid' => $uuid, 'name' => $name];
        if ($nickname !== null) {
            $claims['nickname'] = $nickname;
        }
        if ($avatarUrl !== null) {
            $claims['avatar_url'] = $avatarUrl;
        }
        if ($tokenVersion !== null) {
            $claims['token_version'] = $tokenVersion;
        }
        $claims['iat'] = $issuedAt;
        if ($ttl !== null) {
            $claims['exp'] = $issuedAt + $ttl;
        }

        $payload = self::base64UrlEncode(json_encode($claims, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE));
        $signature = self::base64UrlEncode(hash_hmac('sha256', self::PREFIX . $payload, $secret, true));
        return self::PREFIX . $payload . '.' . $signature;
    }

    /** Decode the claims segment. Throws Error (code -1) on malformed input. */
    public static function decode(string $credential): array
    {
        [$prefix, $payload] = self::segments($credential);
        $claims = json_decode(base64_decode(self::base64UrlPad($payload), true), true);
        if (!is_array($claims)) {
            throw new Error(-1, 'malformed credential: invalid payload', 0);
        }
        return $claims;
    }

    /**
     * Recompute the HMAC over the received payload and compare in constant
     * time. Never throws on malformed input.
     */
    public static function verifySignature(string $credential, string $secret): bool
    {
        try {
            [$prefix, $payload, $signature] = self::segments($credential);
        } catch (\Throwable) {
            return false;
        }
        $expected = hash_hmac('sha256', "{$prefix}.{$payload}", $secret, true);
        $provided = base64_decode(self::base64UrlPad($signature), true);
        return is_string($provided) && hash_equals($expected, $provided);
    }

    /**
     * $exp is a unix-seconds claim; a credential without exp never expires
     * here (the backend treats it the same way). Accepts a credential
     * string or already-decoded claims. $now: injectable unix-seconds clock.
     */
    public static function expired($credentialOrClaims, ?int $now = null): bool
    {
        $claims = is_array($credentialOrClaims) ? $credentialOrClaims : self::decode($credentialOrClaims);
        $exp = $claims['exp'] ?? null;
        return $exp !== null && $exp <= ($now ?? time());
    }

    private static function validateLength(string $value, int $min, int $max, string $field): void
    {
        $length = self::charLength($value);
        if ($length < $min || $length > $max) {
            throw new \InvalidArgumentException("{$field} must be {$min}-{$max} characters");
        }
    }

    private static function charLength(string $value): int
    {
        // Character count without depending on the mbstring extension.
        $count = preg_match_all('/./us', $value);
        return $count === false ? strlen($value) : $count;
    }

    /** @return array{0: string, 1: string, 2: string} [prefix, payload, signature] */
    private static function segments(string $credential): array
    {
        $parts = explode('.', $credential);
        if (count($parts) !== 3 || $parts[0] !== 'im1') {
            throw new Error(-1, 'malformed credential: expected im1.<payload>.<signature>', 0);
        }
        return $parts;
    }

    private static function base64UrlEncode(string $bytes): string
    {
        return rtrim(strtr(base64_encode($bytes), '+/', '-_'), '=');
    }

    private static function base64UrlPad(string $value): string
    {
        $padded = strtr($value, '-_', '+/');
        return $padded . str_repeat('=', (4 - strlen($padded) % 4) % 4);
    }
}
