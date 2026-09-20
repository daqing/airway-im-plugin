<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Client for the admin console API (/admin/api on the public listener).
 * Authenticates with IM_ADMIN_USERNAME / IM_ADMIN_PASSWORD session tokens
 * (12-hour in-memory sessions): the first authenticated call logs in
 * transparently, and a 401 mid-session triggers one re-login and retry.
 *
 *   $admin = new AirwayIM\AdminClient(
 *       apiUrl: 'https://im.example.com',
 *       username: getenv('IM_ADMIN_USERNAME'),
 *       password: getenv('IM_ADMIN_PASSWORD'),
 *   );
 *   $admin->users();
 *   $admin->revokeUser('user-42');
 */
final class AdminClient
{
    private Http $http;
    private string $username;
    private string $password;
    private ?string $token = null;

    public function __construct(string $apiUrl, string $username, string $password, int $timeout = 15)
    {
        $this->http = new Http($apiUrl, $timeout);
        $this->username = $username;
        $this->password = $password;
    }

    /**
     * Exchange admin credentials for a session token (also happens
     * implicitly before the first authenticated call). Returns the login
     * payload {token, expires_at, username}.
     */
    public function login(): array
    {
        $data = (array)$this->http->request('POST', '/admin/api/login', [], null, [
            'username' => $this->username,
            'password' => $this->password,
        ]);
        $this->token = is_string($data['token'] ?? null) ? $data['token'] : null;
        return $data;
    }

    public function logout(): void
    {
        $this->request('POST', '/admin/api/logout');
        $this->token = null;
    }

    /** Aggregated status: database counters plus live gateway/delivery metrics and online user IDs. */
    public function status(): array
    {
        return (array)$this->request('GET', '/admin/api/status');
    }

    /** Registered identities, newest first, with token_version. */
    public function users(): array
    {
        return (array)$this->request('GET', '/admin/api/users');
    }

    /** Revoke a user's outstanding credentials (bumps token_version) and best-effort kick their live gateway connections. */
    public function revokeUser(string $uuid): array
    {
        return (array)$this->request('POST', '/admin/api/users/' . Util::escapeSegment($uuid) . '/revoke');
    }

    /** All group conversations with member/message counts. */
    public function groupConversations(): array
    {
        return (array)$this->request('GET', '/admin/api/conversations');
    }

    /** All messages in a group conversation, ascending sequence order. */
    public function conversationMessages(string $conversationId): array
    {
        return (array)$this->request('GET', '/admin/api/conversations/' . Util::escapeSegment($conversationId) . '/messages');
    }

    /**
     * Mark a message illegal (idempotent): masked to *** for clients, and a
     * message.moderated event fans out to online members.
     */
    public function markIllegal(string $messageId): array
    {
        return (array)$this->request('POST', '/admin/api/messages/' . Util::escapeSegment($messageId) . '/mark-illegal');
    }

    private function request(string $method, string $path)
    {
        $this->ensureLoggedIn();
        try {
            return $this->http->request($method, $path, $this->authHeader());
        } catch (Error $e) {
            if ($e->status !== 401) {
                throw $e;
            }
            // The 12-hour session token expired (or was lost server-side):
            // re-login once and retry a single time.
            $this->token = null;
            $this->ensureLoggedIn();
            return $this->http->request($method, $path, $this->authHeader());
        }
    }

    private function ensureLoggedIn(): void
    {
        if ($this->token === null) {
            $this->login();
        }
    }

    private function authHeader(): array
    {
        return ['Authorization' => 'Bearer ' . $this->token];
    }
}
