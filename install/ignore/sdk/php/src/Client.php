<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * REST client for the Airway IM plugin public API (default port 1905).
 * Authenticates every request with a credential issued by the host
 * backend — this SDK never issues credentials to end users.
 *
 *   $im = new AirwayIM\Client(
 *       apiUrl: 'https://im.example.com',
 *       credential: $hostIssuedCredential,
 *       getCredential: fn () => fetchFreshCredential(), // optional, on 401/10001
 *   );
 *
 * All methods return the envelope's `data` (an array or null) and throw
 * AirwayIM\Error on failure. Conversation-getters return
 * AirwayIM\DirectConversation / AirwayIM\GroupConversation handles — one
 * object per conversation carrying ->id and ->kind (plus ->title on groups).
 * Message responses come back as an AirwayIM\Message — a JSON object with
 * property reads, so $message["id"], $message->id, and
 * $message->sender->uuid all work.
 */
final class Client
{
    public const DEFAULT_CONTENT_TYPE = 'text/markdown';

    private const MIME_TYPES = [
        '.png' => 'image/png', '.jpg' => 'image/jpeg', '.jpeg' => 'image/jpeg',
        '.gif' => 'image/gif', '.webp' => 'image/webp', '.svg' => 'image/svg+xml',
        '.pdf' => 'application/pdf', '.txt' => 'text/plain', '.md' => 'text/markdown',
        '.mp4' => 'video/mp4', '.mov' => 'video/quicktime', '.mp3' => 'audio/mpeg',
        '.zip' => 'application/zip', '.json' => 'application/json',
    ];

    private Http $http;

    /** Current credential; replaced after a successful refresh. */
    public string $credential;

    /** Optional refresh callback; may also be reassigned after construction. PHP properties cannot be typed callable. */
    public $getCredential;

    public function __construct(
        string $apiUrl,
        string $credential,
        ?callable $getCredential = null,
        int $timeout = 15
    ) {
        $this->http = new Http($apiUrl, $timeout);
        $this->credential = $credential;
        $this->getCredential = $getCredential;
    }

    // ---- Identity ----

    public function me(): array
    {
        return (array)$this->get('/api/v1/me');
    }

    // ---- Conversations ----

    /** List my active group conversations (direct ones are excluded by the backend). */
    public function listGroups(): array
    {
        return (array)$this->get('/api/v1/conversations', ['type' => 'group']);
    }

    /**
     * Create or resolve a conversation. $kind: "direct" (get-or-create, may
     * return an existing conversation) or "group" (always creates).
     * $memberUUIDs lists the *other* users by their stable identity uuid;
     * the authenticated user must not be included.
     */
    private function createConversation(string $kind, array $memberUUIDs, ?string $title = null): array
    {
        $body = ['kind' => $kind, 'member_uuids' => $memberUUIDs];
        if ($title !== null) {
            $body['title'] = $title;
        }
        return (array)$this->post('/api/v1/conversations', $body);
    }

    /** Get-or-create the direct conversation with one other user, identified by their uuid. */
    public function createDirect(string $otherUuid): DirectConversation
    {
        $conversation = $this->createConversation('direct', [$otherUuid]);
        return new DirectConversation($this, (string)($conversation['id'] ?? ''));
    }

    /**
     * The direct conversation with one other user, identified by their uuid.
     * Read-only: returns null when none exists yet (createDirect
     * get-or-creates instead). Combine with listMessages()/eachMessage() to
     * poll and display the history with that user.
     */
    public function getDirect(string $otherUuid): ?DirectConversation
    {
        try {
            $conversation = (array)$this->get('/api/v1/conversations/direct/' . Util::escapeSegment($otherUuid));
        } catch (Error $e) {
            if ($e->getCode() !== ErrorCode::CONVERSATION_NOT_FOUND) {
                throw $e;
            }
            return null;
        }
        return new DirectConversation($this, (string)($conversation['id'] ?? ''));
    }

    /** Create a new group; the authenticated user becomes its owner. */
    public function createGroup(?string $title = null, array $memberUUIDs = []): GroupConversation
    {
        $body = ['member_uuids' => $memberUUIDs];
        if ($title !== null) {
            $body['title'] = $title;
        }
        $conversation = (array)$this->post('/api/v1/group', $body);
        return new GroupConversation($this, (string)($conversation['id'] ?? ''), $title);
    }

    /**
     * Conversation kind plus active members with roles.
     *
     * @internal Internal support for openConversation() and
     *   Conversation::details(); read details via Conversation::details() instead.
     */
    public function getConversation(string $conversationId): array
    {
        return (array)$this->get('/api/v1/conversations/' . Util::escapeSegment($conversationId));
    }

    /**
     * Open any conversation by id as a conversation object (e.g. one learned
     * from a message): resolves kind and title from a details lookup.
     */
    public function openConversation(string $id): Conversation
    {
        $details = $this->getConversation($id);
        if (($details['type'] ?? null) === 'group') {
            return new GroupConversation($this, $id, is_string($details['title'] ?? null) ? $details['title'] : null);
        }
        if (($details['type'] ?? null) === 'direct') {
            return new DirectConversation($this, $id);
        }
        return new Conversation($this, $id, (string)($details['type'] ?? ''));
    }

    /** Add members (identified by uuid) to a group (owner/admin). Idempotent for already-active members. */
    public function addMembers(string $conversationId, array $memberUUIDs): array
    {
        return (array)$this->post(
            '/api/v1/conversations/' . Util::escapeSegment($conversationId) . '/members',
            ['member_uuids' => $memberUUIDs]
        );
    }

    /**
     * Remove members (identified by uuid) from a group (owner/admin; cannot
     * remove self or the owner). Idempotent for members who are not active.
     * Returns the details after the last removal (the current details for an
     * empty list).
     */
    public function removeMembers(string $conversationId, array $userUUIDs): array
    {
        $details = $userUUIDs === [] ? $this->getConversation($conversationId) : [];
        foreach ($userUUIDs as $userUUID) {
            $details = $this->removeMember($conversationId, $userUUID);
        }
        return $details;
    }

    /**
     * Remove one member (identified by uuid) from a group (owner/admin;
     * cannot remove self or the owner). Single-member DELETE endpoint —
     * removeMembers() loops over it.
     */
    private function removeMember(string $conversationId, string $userUUID): array
    {
        return (array)$this->delete(
            '/api/v1/conversations/' . Util::escapeSegment($conversationId) . '/members/' . Util::escapeSegment($userUUID)
        );
    }

    // ---- Messages ----

    /**
     * Ordered message page after a sequence; use for history display and
     * reconnect synchronization. $limit: 1-200, the backend falls back to
     * 100 outside that range. Returns Message objects.
     */
    public function listMessages(string $conversationId, ?int $afterSequence = null, ?int $limit = null): array
    {
        $query = [];
        if ($afterSequence !== null) {
            $query['after_sequence'] = $afterSequence;
        }
        if ($limit !== null) {
            $query['limit'] = $limit;
        }
        $rows = (array)$this->get('/api/v1/conversations/' . Util::escapeSegment($conversationId) . '/messages', $query);
        return array_map(static fn (array $row): Message => new Message($row), $rows);
    }

    /**
     * Auto-paging generator over the conversation history in ascending
     * sequence order; stops when a page comes back short or empty.
     *
     *   foreach ($im->eachMessage($conversationId, afterSequence: 42) as $message) { ... }
     */
    public function eachMessage(string $conversationId, int $afterSequence = 0, int $pageSize = 100): \Generator
    {
        $pageSize = min($pageSize, 200);
        $cursor = $afterSequence;
        while (true) {
            $page = $this->listMessages($conversationId, $cursor, $pageSize);
            if ($page === []) {
                return;
            }
            foreach ($page as $message) {
                yield $message;
            }
            $cursor = (int)end($page)['sequence'];
            if (count($page) < $pageSize) {
                return;
            }
        }
    }

    /**
     * Send a message to a group conversation by id. A random Idempotency-Key
     * is generated per call and reused across network-failure retries, so
     * retry storms can never duplicate a message; pass $idempotencyKey to
     * control it (1-128 chars, reuse only for the same logical request).
     */
    public function sendGroupMessage(
        string $conversationId,
        string $content,
        string $contentType = self::DEFAULT_CONTENT_TYPE,
        ?string $idempotencyKey = null,
        int $retries = 1
    ): Message {
        $body = [
            'conversation_id' => $conversationId,
            'content' => $content,
            'content_type' => $contentType,
        ];
        $data = $this->postMessage('/api/v1/messages', $body, $idempotencyKey, $retries);
        return new Message(is_array($data) ? $data : []);
    }

    /**
     * Send a message by conversation id.
     *
     * @internal Internal support for Conversation::send() and
     *   sendDirectMessage(); use sendGroupMessage() / sendDirectMessage() /
     *   Conversation::send() instead.
     */
    public function sendMessage(
        string $conversationId,
        string $content,
        string $contentType = self::DEFAULT_CONTENT_TYPE,
        ?string $idempotencyKey = null,
        int $retries = 1
    ): Message {
        return $this->sendGroupMessage(
            $conversationId,
            $content,
            $contentType,
            $idempotencyKey,
            $retries
        );
    }

    /**
     * Send a direct message to one other user, identified by their uuid:
     * get-or-create the direct conversation, then send. Same idempotency
     * semantics as sendGroupMessage().
     */
    public function sendDirectMessage(
        string $otherUuid,
        string $content,
        string $contentType = self::DEFAULT_CONTENT_TYPE,
        ?string $idempotencyKey = null,
        int $retries = 1
    ): Message {
        $conversation = $this->createDirect($otherUuid);
        return $this->sendMessage(
            $conversation->id,
            $content,
            contentType: $contentType,
            idempotencyKey: $idempotencyKey,
            retries: $retries
        );
    }

    // ---- Storage ----

    /**
     * Upload a file (development-stage API: currently no auth middleware).
     * Accepts a filesystem path or a stream resource; the whole file is
     * read into memory. Returns ["key" => ..., "url" => ..., "size" => ...].
     */
    public function uploadFile($pathOrStream, ?string $filename = null, ?string $dir = null): array
    {
        [$headers, $body] = $this->uploadParts($pathOrStream, $filename, $dir);
        [$status, $payload] = $this->http->transport('POST', '/api/v1/storage', $headers, null, $body);
        if ($status >= 200 && $status < 300 && is_array($payload) && isset($payload['key'])) {
            return $payload;
        }
        $message = is_array($payload) && is_string($payload['error'] ?? null) ? $payload['error'] : null;
        throw new Error(-1, $message ?? "HTTP {$status}: upload failed", $status);
    }

    /** Public download URL for a storage key (no auth, like the API itself). */
    public function storageUrl(string $key): string
    {
        $escaped = implode('/', array_map([Util::class, 'escapeSegment'], explode('/', $key)));
        return $this->http->baseUrl() . '/api/v1/storage/' . $escaped;
    }

    // ---- Internals ----

    private function get(string $path, ?array $query = null)
    {
        return $this->request('GET', $path, [], $query);
    }

    private function post(string $path, array $body)
    {
        return $this->request('POST', $path, [], null, $body);
    }

    private function delete(string $path)
    {
        return $this->request('DELETE', $path);
    }

    private function postMessage(string $path, array $body, ?string $idempotencyKey, int $retries)
    {
        $key = $idempotencyKey ?? self::generateUuid();
        $attempts = 0;
        while (true) {
            try {
                return $this->request('POST', $path, ['Idempotency-Key' => $key], null, $body);
            } catch (Error $e) {
                $attempts++;
                // Retry transport failures (status 0) only; HTTP errors are
                // final and the backend dedupes by the reused idempotency
                // key anyway.
                if ($e->status !== 0 || $attempts > $retries) {
                    throw $e;
                }
            }
        }
    }

    private function request(string $method, string $path, array $headers = [], ?array $query = null, $body = null)
    {
        $authError = null;
        try {
            return $this->http->request($method, $path, $this->withAuth($headers), $query, $body);
        } catch (Error $e) {
            // Credential expired/revoked: refresh once through the host
            // callback and retry a single time. Other errors propagate
            // unchanged.
            if (!$this->refreshable($e)) {
                throw $e;
            }
            $authError = $e;
        }

        $fresh = ($this->getCredential)();
        if (!is_string($fresh) || $fresh === '' || $fresh === $this->credential) {
            throw $authError;
        }

        $this->credential = $fresh;
        return $this->http->request($method, $path, $this->withAuth($headers), $query, $body);
    }

    private function refreshable(Error $error): bool
    {
        return $error->authError()
            && $this->getCredential !== null
            && $this->credential !== '';
    }

    private function withAuth(array $headers): array
    {
        return array_merge(['Authorization' => 'Bearer ' . $this->credential], $headers);
    }

    /**
     * Builds the multipart/form-data parts for uploadFile:
     * [headers, raw body].
     */
    private function uploadParts($pathOrStream, ?string $filename, ?string $dir): array
    {
        [$content, $filename] = $this->readUpload($pathOrStream, $filename);
        $boundary = 'AirwayIM' . bin2hex(random_bytes(16));
        return [
            ['Content-Type' => "multipart/form-data; boundary={$boundary}"],
            $this->multipartBody($content, $filename, $dir, $boundary),
        ];
    }

    /** @return array{0: string, 1: string} [content, filename] */
    private function readUpload($pathOrStream, ?string $filename): array
    {
        if (is_resource($pathOrStream)) {
            $uri = stream_get_meta_data($pathOrStream)['uri'] ?? null;
            $name = $filename
                ?? (is_string($uri) && $uri !== '' && !str_starts_with($uri, 'php://') ? basename($uri) : null)
                ?? 'upload';
            $content = stream_get_contents($pathOrStream);
            if ($content === false) {
                throw new Error(-1, 'failed to read the upload stream', 0);
            }
            return [$content, $name];
        }

        $path = (string)$pathOrStream;
        $content = @file_get_contents($path);
        if ($content === false) {
            throw new Error(-1, "failed to read {$path}", 0);
        }
        return [$content, $filename ?? basename($path)];
    }

    private function multipartBody(string $content, string $filename, ?string $dir, string $boundary): string
    {
        $disposition = 'Content-Disposition: form-data; name="file"; filename="' . self::sanitizeFilename($filename) . '"';
        $body = "--{$boundary}\r\n{$disposition}\r\nContent-Type: " . self::mimeType($filename) . "\r\n\r\n";
        $body .= $content;
        $body .= "\r\n";
        if ($dir !== null) {
            $body .= "--{$boundary}\r\nContent-Disposition: form-data; name=\"dir\"\r\n\r\n{$dir}\r\n";
        }
        $body .= "--{$boundary}--\r\n";
        return $body;
    }

    private static function sanitizeFilename(string $filename): string
    {
        return preg_replace('/[\r\n"\\\\]/', '_', $filename);
    }

    private static function mimeType(string $filename): string
    {
        $extension = strtolower(pathinfo($filename, PATHINFO_EXTENSION));
        return $extension === '' ? 'application/octet-stream' : (self::MIME_TYPES[".$extension"] ?? 'application/octet-stream');
    }

    private static function generateUuid(): string
    {
        $bytes = random_bytes(16);
        $bytes[6] = chr((ord($bytes[6]) & 0x0f) | 0x40);
        $bytes[8] = chr((ord($bytes[8]) & 0x3f) | 0x80);
        return vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($bytes), 4));
    }
}
