<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Server-to-server client for the plugin's internal API listener
 * (IM_INTERNAL_ADDR, default 127.0.0.1:1906, secret-protected). Reachable
 * only from a private network — never from end-user clients.
 *
 * Mint credentials on behalf of your users without holding IM_AUTH_SECRET:
 *
 *   $internal = new AirwayIM\InternalClient(
 *       internalUrl: 'http://127.0.0.1:1906',
 *       internalSecret: getenv('IM_INTERNAL_SECRET'),
 *   );
 *   $minted = $internal->mintCredential(uuid: 'user-42', name: 'alice');
 *
 * Minted credentials carry the user's current token_version and are
 * therefore revocable through the admin API.
 */
final class InternalClient
{
    private Http $http;
    private string $internalSecret;

    public function __construct(string $internalUrl, string $internalSecret, int $timeout = 15)
    {
        $this->http = new Http($internalUrl, $timeout);
        $this->internalSecret = $internalSecret;
    }

    /**
     * Mint a credential for (uuid, name). $ttlSeconds defaults to 86400
     * (24 h) on the backend and is capped at 2592000 (30 days); 0 omits the
     * expiry claim. The user is registered (or profile-refreshed) at mint
     * time.
     */
    public function mintCredential(
        string $uuid,
        string $name,
        ?string $nickname = null,
        ?string $avatarUrl = null,
        ?int $ttlSeconds = null
    ): MintedCredential {
        $body = ['uuid' => $uuid, 'name' => $name];
        if ($nickname !== null) {
            $body['nickname'] = $nickname;
        }
        if ($avatarUrl !== null) {
            $body['avatar_url'] = $avatarUrl;
        }
        if ($ttlSeconds !== null) {
            $body['ttl_seconds'] = $ttlSeconds;
        }

        $data = (array)$this->http->request(
            'POST',
            '/internal/v1/credentials',
            ['X-IM-Internal-Secret' => $this->internalSecret],
            null,
            $body
        );
        return new MintedCredential(
            (string)($data['credential'] ?? ''),
            is_string($data['expires_at'] ?? null) ? $data['expires_at'] : null
        );
    }
}
