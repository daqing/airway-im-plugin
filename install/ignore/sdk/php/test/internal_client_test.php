<?php

declare(strict_types=1);

use AirwayIM\Error;
use AirwayIM\InternalClient;

final class InternalClientTest extends TestCase
{
    private const SECRET = 'internal-secret';

    public function testMintCredentialSendsInternalSecretAndParsesResponse(): void
    {
        $credential = 'im1.eyJ1dWlkIjoidXNlci00MiJ9.sig';
        $this->startServer([['status' => 200, 'body' => envelope([
            'credential' => $credential, 'expires_at' => '2026-09-20T08:00:00Z',
        ])]]);

        $minted = (new InternalClient(
            internalUrl: $this->server->url(),
            internalSecret: self::SECRET
        ))->mintCredential(uuid: 'user-42', name: 'alice', nickname: 'Alice', ttlSeconds: 3600);

        assertSame($credential, $minted->credential());
        assertSame('2026-09-20T08:00:00Z', $minted->expiresAt());

        $request = $this->server->requests()[0];
        assertSame('POST', $request['method']);
        assertSame('/internal/v1/credentials', $request['path']);
        assertSame(self::SECRET, $request['headers']['x-im-internal-secret']);
        assertEquals(
            ['uuid' => 'user-42', 'name' => 'alice', 'nickname' => 'Alice', 'ttl_seconds' => 3600],
            json_decode($request['body'], true)
        );
    }

    public function testNotifyUsersSendsInternalSecretAndReturnsEventId(): void
    {
        $this->startServer([['status' => 200, 'body' => envelope(['event_id' => '01JFR8ZK6V'])]]);

        $eventId = (new InternalClient(
            internalUrl: $this->server->url(),
            internalSecret: self::SECRET
        ))->notifyUsers(['user-1', 'user-2'], 'host.friend_request', ['from_uuid' => 'user-1']);

        assertSame('01JFR8ZK6V', $eventId);

        $request = $this->server->requests()[0];
        assertSame('POST', $request['method']);
        assertSame('/internal/v1/notify', $request['path']);
        assertSame(self::SECRET, $request['headers']['x-im-internal-secret']);
        assertEquals(
            ['user_uuids' => ['user-1', 'user-2'], 'event' => 'host.friend_request', 'data' => ['from_uuid' => 'user-1']],
            json_decode($request['body'], true)
        );
    }

    public function testNotifyErrorsMapEnvelopeCodes(): void
    {
        foreach ([
            [400, 10003, 'notify: at most 100 recipients per event'],
            [401, 10005, 'Internal authentication required'],
        ] as [$status, $code, $message]) {
            $this->startServer([['status' => $status, 'body' => envelope(null, $code, $message)]]);

            $error = assertThrows(
                fn () => (new InternalClient(internalUrl: $this->server->url(), internalSecret: self::SECRET))
                    ->notifyUsers(['user-1'], 'host.friend_request', ['from_uuid' => 'user-2']),
                Error::class
            );
            assertSame($code, $error->getCode());
            assertSame($status, $error->status);
            assertSame($message, $error->getMessage());

            $this->server->stop();
            $this->server = null;
        }
    }

    public function testMintCredentialOmitsNullFields(): void
    {
        $this->startServer([['status' => 200, 'body' => envelope(['credential' => 'im1.x.y', 'expires_at' => null])]]);

        $minted = (new InternalClient(
            internalUrl: $this->server->url(),
            internalSecret: self::SECRET
        ))->mintCredential(uuid: 'user-42', name: 'alice');

        assertSame('im1.x.y', $minted->credential());
        assertNull($minted->expiresAt());
        assertEquals(['uuid' => 'user-42', 'name' => 'alice'], json_decode($this->server->requests()[0]['body'], true));
    }

    public function testMintErrorsMapEnvelopeCodes(): void
    {
        foreach ([
            [400, 10003, 'Invalid JSON body'],
            [401, 10005, 'Internal authentication required'],
            [503, 10006, 'Credential signing is not configured'],
        ] as [$status, $code, $message]) {
            $this->startServer([['status' => $status, 'body' => envelope(null, $code, $message)]]);

            $error = assertThrows(
                fn () => (new InternalClient(internalUrl: $this->server->url(), internalSecret: self::SECRET))
                    ->mintCredential(uuid: 'u', name: 'n'),
                Error::class
            );
            assertSame($code, $error->getCode());
            assertSame($status, $error->status);
            assertSame($message, $error->getMessage());

            $this->server->stop();
            $this->server = null;
        }
    }
}
