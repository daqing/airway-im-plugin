<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Conversation object: one object per conversation carrying its id
 * and kind, mirroring the TS SDK's Conversation minus the realtime
 * layer (the server-side SDKs poll the sequence-based sync API instead of
 * subscribing to gateway events).
 *
 *   $direct = $im->createDirect('user-2');
 *   $direct->send('hi');
 *   foreach ($direct->eachMessage() as $message) { ... }
 */
class Conversation
{
    public function __construct(
        protected Client $client,
        public string $id,
        public string $kind
    ) {
    }

    /** Send a message to this conversation (idempotency semantics as sendMessage). */
    public function send(
        string $content,
        string $contentType = Client::DEFAULT_CONTENT_TYPE,
        ?string $idempotencyKey = null,
        int $retries = 1
    ): Message {
        return $this->client->sendMessage($this->id, $content, $contentType, $idempotencyKey, $retries);
    }

    /** Ordered message page in this conversation (listMessages semantics). */
    public function listMessages(?int $afterSequence = null, ?int $limit = null): array
    {
        return $this->client->listMessages($this->id, $afterSequence, $limit);
    }

    /** Auto-paging generator over this conversation's history (eachMessage semantics). */
    public function eachMessage(int $afterSequence = 0, int $pageSize = 100): \Generator
    {
        return $this->client->eachMessage($this->id, $afterSequence, $pageSize);
    }

    /** Conversation kind plus members with roles, fresh from the API. */
    public function details(): array
    {
        return $this->client->getConversation($this->id);
    }
}
