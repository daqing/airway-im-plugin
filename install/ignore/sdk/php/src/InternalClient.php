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
 *
 * It also pushes host-domain notifications (friend requests, mail alerts,
 * …) to your users over the IM WebSocket gateway:
 *
 *   $eventId = $internal->notifyUsers(
 *       ['user-2'], 'host.friend_request', ['from_uuid' => 'user-1'],
 *   );
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

    /**
     * Push a host-domain notification to the given users over the IM
     * WebSocket gateway; returns the event id. $event is a free-form name
     * (conventionally prefixed "host.", e.g. "host.friend_request") and
     * $data any JSON object. Server-side limits: at most 100 recipients,
     * event name 1–64 chars, encoded data at most 4 KiB. Delivery is
     * advisory — online recipients receive the frame in real time, offline
     * recipients are not replayed, so pair notifications with a pull
     * endpoint clients can load on demand.
     */
    public function notifyUsers(array $userUuids, string $event, array $data): string
    {
        $response = (array)$this->http->request(
            'POST',
            '/internal/v1/notify',
            ['X-IM-Internal-Secret' => $this->internalSecret],
            null,
            ['user_uuids' => array_values($userUuids), 'event' => $event, 'data' => $data]
        );
        return (string)($response['event_id'] ?? '');
    }
}
