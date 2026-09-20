<?php

declare(strict_types=1);

use AirwayIM\AdminClient;
use AirwayIM\Error;

final class AdminClientTest extends TestCase
{
    private const USERNAME = 'admin';
    private const PASSWORD = 'secret';

    private function withServer(array $rules): AdminClient
    {
        $this->startServer($rules);
        return new AdminClient(
            apiUrl: $this->server->url(),
            username: self::USERNAME,
            password: self::PASSWORD
        );
    }

    private function loginOk(): array
    {
        return ['path' => '/admin/api/login', 'status' => 200, 'body' => envelope([
            'token' => 'session-token', 'expires_at' => '2026-09-20T08:00:00Z', 'username' => self::USERNAME,
        ])];
    }

    public function testLoginReturnsSessionPayload(): void
    {
        $client = $this->withServer([$this->loginOk()]);

        $data = $client->login();
        assertSame('session-token', $data['token']);

        $request = $this->server->requests()[0];
        assertSame('POST', $request['method']);
        assertSame('/admin/api/login', $request['path']);
        assertEquals(['username' => self::USERNAME, 'password' => self::PASSWORD], json_decode($request['body'], true));
    }

    public function testFirstAuthenticatedCallLogsInImplicitly(): void
    {
        $client = $this->withServer([
            $this->loginOk(),
            ['path' => '/admin/api/users', 'status' => 200, 'body' => envelope([
                ['id' => 1, 'uuid' => 'user-42', 'token_version' => 1],
            ])],
        ]);

        $users = $client->users();
        assertSame(1, count($users));
        assertSame('Bearer session-token', $this->server->requests()[1]['headers']['authorization']);

        $paths = array_column($this->server->requests(), 'path');
        assertSame(['/admin/api/login', '/admin/api/users'], $paths);
    }

    public function test401TriggersSingleReloginAndRetry(): void
    {
        $client = $this->withServer([
            $this->loginOk(),
            ['path' => '/admin/api/users', 'max' => 1, 'status' => 401, 'body' => envelope(null, 20001, 'Administrator authentication required')],
            ['path' => '/admin/api/users', 'status' => 200, 'body' => envelope([])],
        ]);

        assertSame([], $client->users());

        $requests = $this->server->requests();
        assertSame(4, count($requests)); // login, users(401), re-login, users again
        $logins = count(array_filter($requests, static fn (array $r) => $r['path'] === '/admin/api/login'));
        $userCalls = count(array_filter($requests, static fn (array $r) => $r['path'] === '/admin/api/users'));
        assertSame(2, $logins);
        assertSame(2, $userCalls);
    }

    public function testReloginFailurePropagates(): void
    {
        $client = $this->withServer([
            $this->loginOk(),
            ['path' => '/admin/api/users', 'status' => 401, 'body' => envelope(null, 20001, 'Administrator authentication required')],
        ]);

        $error = assertThrows(fn () => $client->users(), Error::class);
        assertSame(401, $error->status);
        assertSame(4, count($this->server->requests())); // login, users(401), re-login, users(401 again)
    }

    public function testAdminOperationsTargetTheRightPaths(): void
    {
        $client = $this->withServer([
            $this->loginOk(),
            ['path' => '/admin/api/users/user-42/revoke', 'status' => 200, 'body' => envelope(['uuid' => 'user-42', 'token_version' => 2, 'connections_kicked' => 1])],
            ['path' => '/admin/api/status', 'status' => 200, 'body' => envelope(['users' => ['total' => 1]])],
            ['path' => '/admin/api/conversations', 'status' => 200, 'body' => envelope([])],
            ['path' => '/admin/api/conversations/01AB/messages', 'status' => 200, 'body' => envelope([])],
            ['path' => '/admin/api/messages/01M1/mark-illegal', 'status' => 200, 'body' => envelope(['id' => '01M1', 'is_illegal' => true])],
        ]);

        $client->revokeUser('user-42');
        $client->status();
        $client->groupConversations();
        $client->conversationMessages('01AB');
        $client->markIllegal('01M1');

        assertSame(6, count($this->server->requests()));
        assertSame('POST', $this->server->requests()[1]['method']);
        assertSame('POST', $this->server->requests()[5]['method']);
    }

    public function testLogoutClearsSessionAndNextCallLogsInAgain(): void
    {
        $client = $this->withServer([
            $this->loginOk(),
            ['path' => '/admin/api/logout', 'status' => 200, 'body' => envelope(null)],
            ['path' => '/admin/api/users', 'status' => 200, 'body' => envelope([])],
        ]);

        $client->logout();
        $client->users();

        $paths = array_column($this->server->requests(), 'path');
        assertSame(['/admin/api/login', '/admin/api/logout', '/admin/api/login', '/admin/api/users'], $paths);
        assertSame('Bearer session-token', $this->server->requests()[3]['headers']['authorization']);
    }
}
