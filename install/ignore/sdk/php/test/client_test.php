<?php

declare(strict_types=1);

use AirwayIM\Client;
use AirwayIM\DirectConversation;
use AirwayIM\Error;
use AirwayIM\GroupConversation;
use AirwayIM\Message;

final class ClientTest extends TestCase
{
    private const CREDENTIAL = 'im1.eyJ1dWlkIjoidXNlci00MiJ9.aaa';
    private const FRESH_CREDENTIAL = 'im1.eyJ1dWlkIjoidXNlci00MiJ9.fresh';

    private function withServer(array $rules): Client
    {
        $this->startServer($rules);
        return new Client(apiUrl: $this->server->url(), credential: self::CREDENTIAL);
    }

    public function testMeSendsBearerTokenAndUnwrapsEnvelope(): void
    {
        $user = ['id' => 1, 'uuid' => 'user-42', 'username' => 'alice', 'nickname' => 'Alice'];
        $client = $this->withServer([['status' => 200, 'body' => envelope($user)]]);

        assertSame($user, $client->me());

        $request = $this->server->requests()[0];
        assertSame('GET', $request['method']);
        assertSame('/api/v1/me', $request['path']);
        assertSame('Bearer ' . self::CREDENTIAL, $request['headers']['authorization']);
    }

    public function testListGroupsBuildsTypeQuery(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope([])]]);

        assertSame([], $client->listGroups());
        assertSame('/api/v1/conversations?type=group', $this->server->requests()[0]['path']);
    }

    public function testCreateDirectReturnsHandleAndPostsGetOrCreateBody(): void
    {
        $client = $this->withServer([['status' => 201, 'body' => envelope(['id' => '01AB', 'kind' => 'direct'])]]);

        $direct = $client->createDirect('uuid-bob');
        assertInstanceOf(DirectConversation::class, $direct);
        assertSame('01AB', $direct->id);
        assertSame('direct', $direct->kind);

        $request = $this->server->requests()[0];
        assertSame('POST', $request['method']);
        assertSame('/api/v1/conversations', $request['path']);
        assertEquals(['kind' => 'direct', 'member_uuids' => ['uuid-bob']], json_decode($request['body'], true));
    }

    public function testCreateGroupConversationOmitsNullTitle(): void
    {
        $client = $this->withServer([['path' => '/api/v1/group', 'status' => 201, 'body' => envelope(['id' => '01AC', 'kind' => 'group'])]]);

        $group = $client->createGroup(memberUUIDs: ['uuid-bob', 'uuid-carol']);
        assertInstanceOf(GroupConversation::class, $group);
        assertSame('01AC', $group->id);
        assertSame('group', $group->kind);
        assertNull($group->title);

        assertEquals(
            ['member_uuids' => ['uuid-bob', 'uuid-carol']],
            json_decode($this->server->requests()[0]['body'], true)
        );
    }

    public function testCreateGroupConversationCarriesTitle(): void
    {
        $client = $this->withServer([['path' => '/api/v1/group', 'status' => 201, 'body' => envelope(['id' => '01AC', 'kind' => 'group'])]]);

        $group = $client->createGroup('Backend Team', ['uuid-bob', 'uuid-carol']);
        assertSame('Backend Team', $group->title);

        assertEquals(
            ['title' => 'Backend Team', 'member_uuids' => ['uuid-bob', 'uuid-carol']],
            json_decode($this->server->requests()[0]['body'], true)
        );
    }

    public function testGetDirectConversationReturnsHandleWhenPresent(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope(['id' => '01AB', 'kind' => 'direct', 'title' => null])]]);

        $direct = $client->getDirect('uuid-bob');
        assertInstanceOf(DirectConversation::class, $direct);
        assertSame('01AB', $direct->id);
        assertSame('/api/v1/conversations/direct/uuid-bob', $this->server->requests()[0]['path']);
    }

    public function testGetDirectConversationReturnsNullWhenAbsent(): void
    {
        $client = $this->withServer([['status' => 404, 'body' => envelope(null, 11001, 'Conversation not found')]]);

        assertNull($client->getDirect('uuid-bob'));
    }

    public function testGetDirectConversationRaisesOtherErrors(): void
    {
        $client = $this->withServer([['status' => 500, 'body' => envelope(null, 10000, 'boom')]]);

        $error = assertThrows(fn () => $client->getDirect('uuid-bob'), Error::class);
        assertSame(10000, $error->getCode());
    }

    public function testOpenConversationResolvesKindFromDetails(): void
    {
        $client = $this->withServer([
            ['path' => '/api/v1/conversations/01AB', 'status' => 200, 'body' => envelope(['conversation_uuid' => '01AB', 'type' => 'group', 'title' => 'Team', 'members' => []])],
            ['path' => '/api/v1/conversations/01CD', 'status' => 200, 'body' => envelope(['conversation_uuid' => '01CD', 'type' => 'direct', 'members' => []])],
        ]);

        $group = $client->openConversation('01AB');
        assertInstanceOf(GroupConversation::class, $group);
        assertSame('01AB', $group->id);
        assertSame('Team', $group->title);

        $direct = $client->openConversation('01CD');
        assertInstanceOf(DirectConversation::class, $direct);
        assertSame('01CD', $direct->id);
    }

    public function testGroupHandleCarriesGroupOperations(): void
    {
        $client = $this->withServer([
            ['path' => '/api/v1/group', 'status' => 201, 'body' => envelope(['id' => '01AC', 'kind' => 'group'])],
            ['path' => '/api/v1/conversations/01AC/members', 'status' => 200, 'body' => envelope(['conversation_uuid' => '01AC', 'type' => 'group', 'members' => []])],
            ['path' => '/api/v1/conversations/01AC/members/uuid-carol', 'status' => 200, 'body' => envelope(['conversation_uuid' => '01AC', 'type' => 'group', 'members' => []])],
            ['path' => '/api/v1/conversations/01AC', 'status' => 200, 'body' => envelope(['conversation_uuid' => '01AC', 'type' => 'group', 'members' => []])],
        ]);

        $group = $client->createGroup('Team', ['uuid-bob']);
        $group->addMembers(['uuid-carol']);
        $group->removeMembers(['uuid-carol']);
        assertEquals(
            ['conversation_uuid' => '01AC', 'type' => 'group', 'members' => []],
            $group->details()
        );

        $requests = $this->server->requests();
        assertSame('/api/v1/conversations/01AC/members', $requests[1]['path']);
        assertSame('/api/v1/conversations/01AC/members/uuid-carol', $requests[2]['path']);
        assertSame('/api/v1/conversations/01AC', $requests[3]['path']);
    }

    public function testConversationAndMemberManagementEscapeIds(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope(['conversation_uuid' => '01 AB', 'type' => 'group', 'members' => []])]]);

        $client->openConversation('01 AB')->details();
        $client->addMembers('01 AB', ['uuid-carol']);
        $client->removeMembers('01 AB', ['uuid carol']);

        $requests = $this->server->requests();
        assertSame('/api/v1/conversations/01%20AB', $requests[0]['path']);
        assertSame('/api/v1/conversations/01%20AB', $requests[1]['path']); // details()
        assertSame('/api/v1/conversations/01%20AB/members', $requests[2]['path']);
        assertSame('/api/v1/conversations/01%20AB/members/uuid%20carol', $requests[3]['path']);
    }

    public function testListMessagesSendsSequenceQuery(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope([])]]);

        assertSame([], $client->listMessages('01AB', afterSequence: 42, limit: 10));
        assertSame('/api/v1/conversations/01AB/messages?after_sequence=42&limit=10', $this->server->requests()[0]['path']);
    }

    public function testListMessagesWithoutOptionsHasNoQuery(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope([])]]);

        $client->listMessages('01AB');
        assertSame('/api/v1/conversations/01AB/messages', $this->server->requests()[0]['path']);
    }

    public function testEachMessagePagesUntilShortPage(): void
    {
        $client = $this->withServer([
            ['max' => 1, 'status' => 200, 'body' => envelope([$this->msg(1), $this->msg(2)])],
            ['max' => 1, 'status' => 200, 'body' => envelope([$this->msg(3)])],
        ]);

        $collected = iterator_to_array($client->eachMessage('01AB', pageSize: 2), false);
        assertSame([1, 2, 3], array_map(static fn (Message $m) => $m['sequence'], $collected));

        $requests = $this->server->requests();
        assertSame('/api/v1/conversations/01AB/messages?after_sequence=0&limit=2', $requests[0]['path']);
        assertSame('/api/v1/conversations/01AB/messages?after_sequence=2&limit=2', $requests[1]['path']);
    }

    public function testEachMessageStopsOnEmptyPage(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope([])]]);

        assertSame([], iterator_to_array($client->eachMessage('01AB', afterSequence: 99), false));
    }

    public function testEachMessageClampsPageSizeTo200(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope([])]]);

        iterator_to_array($client->eachMessage('01AB', pageSize: 300), false);
        assertSame('/api/v1/conversations/01AB/messages?after_sequence=0&limit=200', $this->server->requests()[0]['path']);
    }

    public function testHandleDelegatesToClientCalls(): void
    {
        $client = $this->withServer([
            ['path' => '/api/v1/conversations', 'status' => 201, 'body' => envelope(['id' => '01AB', 'kind' => 'direct'])],
            ['path' => '/api/v1/messages', 'max' => 1, 'status' => 201, 'body' => envelope($this->msg(1))],
            ['path' => '/api/v1/conversations/01AB/messages?after_sequence=0&limit=100', 'status' => 200, 'body' => envelope([$this->msg(1)])],
        ]);

        $direct = $client->createDirect('uuid-bob');
        $message = $direct->send('hi');
        assertInstanceOf(Message::class, $message);
        assertSame('01AB', $message->conversation_id);
        assertTrue(isset($this->server->requests()[1]['headers']['idempotency-key']), 'handle send carries an idempotency key');

        $history = iterator_to_array($direct->eachMessage(), false);
        assertSame([1], array_map(static fn (Message $m) => $m->sequence, $history));
        assertSame('/api/v1/conversations/01AB/messages?after_sequence=0&limit=100', $this->server->requests()[2]['path']);
    }

    public function testHandleListMessagesForwardsPaging(): void
    {
        $client = $this->withServer([
            ['path' => '/api/v1/conversations', 'status' => 201, 'body' => envelope(['id' => '01AB', 'kind' => 'direct'])],
            ['status' => 200, 'body' => envelope([])],
        ]);

        $direct = $client->createDirect('uuid-bob');
        $direct->listMessages(afterSequence: 7, limit: 3);

        assertSame('/api/v1/conversations/01AB/messages?after_sequence=7&limit=3', $this->server->requests()[1]['path']);
    }

    public function testSendGroupMessageGeneratesIdempotencyKey(): void
    {
        $message = ['id' => '01M1', 'sequence' => 1, 'content' => 'hi'];
        $client = $this->withServer([['status' => 201, 'body' => envelope($message)]]);

        assertSame($message, $client->sendGroupMessage('01AB', 'hi')->toArray());

        $request = $this->server->requests()[0];
        $key = $request['headers']['idempotency-key'] ?? '';
        assertTrue(strlen($key) >= 1 && strlen($key) <= 128, 'idempotency key length must be 1-128');
        assertEquals(
            ['conversation_id' => '01AB', 'content' => 'hi', 'content_type' => 'text/markdown'],
            json_decode($request['body'], true)
        );
    }

    public function testSendGroupMessageRetriesTransportFailureWithSameKey(): void
    {
        $client = $this->withServer([
            ['max' => 1, 'abort' => true],
            ['status' => 201, 'body' => envelope(['id' => '01M1', 'sequence' => 1])],
        ]);

        $message = $client->sendGroupMessage('01AB', 'hi');
        assertSame('01M1', $message['id']);
        assertSame(2, count($this->server->requests()));
        $keys = array_column($this->server->requests(), 'headers');
        assertSame(1, count(array_unique(array_column($keys, 'idempotency-key'))));
    }

    public function testSendGroupMessageDoesNotRetryHttpErrors(): void
    {
        $client = $this->withServer([[
            'status' => 409,
            'body' => envelope(null, 11002, 'Idempotency key was reused with a different request'),
        ]]);

        $error = assertThrows(fn () => $client->sendGroupMessage('01AB', 'hi'), Error::class);
        assertSame(11002, $error->getCode());
        assertSame(409, $error->status);
        assertSame(1, count($this->server->requests()));
    }

    public function testSendGroupMessageHonorsExplicitKeyAndContentType(): void
    {
        $client = $this->withServer([['status' => 201, 'body' => envelope([])]]);

        $client->sendGroupMessage('01AB', 'hi', contentType: 'text/plain', idempotencyKey: 'my-key');

        $request = $this->server->requests()[0];
        assertSame('my-key', $request['headers']['idempotency-key']);
        assertEquals(
            ['conversation_id' => '01AB', 'content' => 'hi', 'content_type' => 'text/plain'],
            json_decode($request['body'], true)
        );
    }

    public function testSendGroupMessageReturnsMessageWithPropertyReads(): void
    {
        $payload = ['id' => '01M1', 'conversation_id' => '01AB', 'sequence' => 1,
            'content' => 'hi', 'content_type' => 'text/markdown',
            'created_at' => '2026-01-01T00:00:00Z',
            'sender' => ['uuid' => 'uuid-alice', 'username' => 'alice',
                'nickname' => 'Alice', 'avatar_url' => null]];
        $client = $this->withServer([['status' => 201, 'body' => envelope($payload)]]);

        $message = $client->sendGroupMessage('01AB', 'hi');
        assertInstanceOf(Message::class, $message);
        assertSame('01M1', $message->id);
        assertSame('01AB', $message->conversation_id);
        assertSame('hi', $message->content);
        assertSame('text/markdown', $message->content_type);
        assertSame(1, $message->sequence);
        assertSame('2026-01-01T00:00:00Z', $message->created_at);
        assertSame('uuid-alice', $message->sender->uuid);
        assertSame('alice', $message->sender->username);
        assertSame('Alice', $message->sender->nickname);
        assertNull($message->sender->avatar_url);
        assertSame('hi', $message['content']);
        assertSame($payload, json_decode(json_encode($message), true));
    }

    public function testListMessagesReturnsMessageObjects(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope([$this->msg(1), $this->msg(2)])]]);

        $list = $client->listMessages('01AB');
        assertSame(['01M1', '01M2'], array_map(static fn (Message $m) => $m->id, $list));
        assertSame([1, 2], array_map(static fn (Message $m) => $m->sequence, $list));
        assertSame('alice', $list[0]->sender->username);
    }

    public function testSendDirectMessageCreatesConversationThenSends(): void
    {
        $client = $this->withServer([
            ['path' => '/api/v1/conversations', 'status' => 201, 'body' => envelope(['id' => '01AB', 'kind' => 'direct'])],
            ['path' => '/api/v1/messages', 'status' => 201, 'body' => envelope(['id' => '01M1', 'sequence' => 1])],
        ]);

        $message = $client->sendDirectMessage('uuid-bob', 'hi');
        assertSame('01M1', $message['id']);

        $requests = $this->server->requests();
        assertSame('/api/v1/conversations', $requests[0]['path']);
        assertSame('/api/v1/messages', $requests[1]['path']);
        assertEquals(['kind' => 'direct', 'member_uuids' => ['uuid-bob']], json_decode($requests[0]['body'], true));
        assertEquals(
            ['conversation_id' => '01AB', 'content' => 'hi', 'content_type' => 'text/markdown'],
            json_decode($requests[1]['body'], true)
        );
    }

    public function testSendDirectMessagePassesOptionsToSend(): void
    {
        $client = $this->withServer([
            ['path' => '/api/v1/conversations', 'status' => 201, 'body' => envelope(['id' => '01AB', 'kind' => 'direct'])],
            ['path' => '/api/v1/messages', 'status' => 201, 'body' => envelope([])],
        ]);

        $client->sendDirectMessage('uuid-bob', 'hi', contentType: 'text/plain', idempotencyKey: 'my-key');

        $request = $this->server->requests()[1];
        assertSame('my-key', $request['headers']['idempotency-key']);
        assertEquals(
            ['conversation_id' => '01AB', 'content' => 'hi', 'content_type' => 'text/plain'],
            json_decode($request['body'], true)
        );
    }

    public function testAuthErrorRefreshesCredentialAndRetriesOnce(): void
    {
        $client = $this->withServer([
            ['max' => 1, 'status' => 400, 'body' => envelope(null, 10001, 'Invalid bearer token')],
            ['status' => 200, 'body' => envelope(['id' => 1])],
        ]);
        $client->getCredential = fn (): string => self::FRESH_CREDENTIAL;

        assertSame(['id' => 1], $client->me());
        assertSame(2, count($this->server->requests()));
        assertSame('Bearer ' . self::CREDENTIAL, $this->server->requests()[0]['headers']['authorization']);
        assertSame('Bearer ' . self::FRESH_CREDENTIAL, $this->server->requests()[1]['headers']['authorization']);
        assertSame(self::FRESH_CREDENTIAL, $client->credential);
    }

    public function testNoRefreshWithoutCallback(): void
    {
        $client = $this->withServer([['status' => 400, 'body' => envelope(null, 10001, 'Invalid bearer token')]]);

        assertThrows(fn () => $client->me(), Error::class);
        assertSame(1, count($this->server->requests()));
    }

    public function testNoRefreshWhenCallbackReturnsSameCredential(): void
    {
        $client = $this->withServer([['status' => 400, 'body' => envelope(null, 10001, 'Invalid bearer token')]]);
        $client->getCredential = fn (): string => self::CREDENTIAL;

        assertThrows(fn () => $client->me(), Error::class);
        assertSame(1, count($this->server->requests()));
    }

    public function testMapsEnvelopeErrorsWithStatus(): void
    {
        $client = $this->withServer([['status' => 403, 'body' => envelope(null, 10005, 'Permission denied')]]);

        $error = assertThrows(fn () => $client->listGroups(), Error::class);
        assertSame(10005, $error->getCode());
        assertSame(403, $error->status);
        assertSame('Permission denied', $error->getMessage());
        assertFalse($error->authError());
    }

    public function testNonEnvelopeBodyMapsToCodeMinusOne(): void
    {
        $client = $this->withServer([['status' => 500, 'body' => json_encode(['error' => 'boom'])]]);

        $error = assertThrows(fn () => $client->me(), Error::class);
        assertSame(-1, $error->getCode());
        assertSame(500, $error->status);
        assertFalse($error->authError());
    }

    public function testTransportFailureMapsToStatusZero(): void
    {
        $client = $this->withServer([['abort' => true]]);

        $error = assertThrows(fn () => $client->me(), Error::class);
        assertSame(-1, $error->getCode());
        assertSame(0, $error->status);
    }

    public function testStorageUrlEscapesSegments(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => envelope(null)]]);

        assertSame(
            $this->server->url() . '/api/v1/storage/attachments/202607/a%20b.png',
            $client->storageUrl('attachments/202607/a b.png')
        );
    }

    public function testUploadFileSendsMultipartAndParsesResult(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => json_encode(['key' => 'a/1.txt', 'url' => 'http://x/1.txt', 'size' => 12])]]);

        $tmp = tempnam(sys_get_temp_dir(), 'aim-upload-');
        file_put_contents($tmp, 'hello upload');
        try {
            $result = $client->uploadFile($tmp, filename: 'hello.txt', dir: 'attachments');
        } finally {
            @unlink($tmp);
        }

        assertEquals(['key' => 'a/1.txt', 'url' => 'http://x/1.txt', 'size' => 12], $result);

        $request = $this->server->requests()[0];
        assertSame('POST', $request['method']);
        assertSame('/api/v1/storage', $request['path']);
        $contentType = $request['headers']['content-type'] ?? '';
        assertTrue(str_starts_with($contentType, 'multipart/form-data; boundary='), 'must send multipart/form-data');
        $boundary = substr($contentType, strlen('multipart/form-data; boundary='));
        $body = $request['body'];
        assertStringContains("--{$boundary}", $body);
        assertStringContains('name="file"; filename="hello.txt"', $body);
        assertStringContains('Content-Type: text/plain', $body);
        assertStringContains('hello upload', $body);
        assertStringContains('name="dir"', $body);
        assertStringContains('attachments', $body);
    }

    public function testUploadFileAcceptsStreamWithExplicitFilename(): void
    {
        $client = $this->withServer([['status' => 200, 'body' => json_encode(['key' => 'k', 'url' => '', 'size' => 4])]]);

        $stream = fopen('php://temp', 'w+b');
        fwrite($stream, '%PDF');
        rewind($stream);
        $result = $client->uploadFile($stream, filename: 'report.pdf');
        fclose($stream);

        assertSame('k', $result['key']);
        $body = $this->server->requests()[0]['body'];
        assertStringContains('name="file"; filename="report.pdf"', $body);
        assertStringContains('Content-Type: application/pdf', $body);
    }

    public function testUploadErrorRaises(): void
    {
        $client = $this->withServer([['status' => 400, 'body' => json_encode(['error' => 'no file'])]]);

        $stream = fopen('php://temp', 'w+b');
        fwrite($stream, 'x');
        rewind($stream);
        $error = assertThrows(fn () => $client->uploadFile($stream, filename: 'x.txt'), Error::class);
        fclose($stream);
        assertSame(-1, $error->getCode());
        assertSame('no file', $error->getMessage());
    }

    private function msg(int $sequence): array
    {
        return ['id' => "01M{$sequence}", 'conversation_id' => '01AB',
            'sender' => ['uuid' => 'uuid-alice', 'username' => 'alice',
                'nickname' => 'Alice', 'avatar_url' => null],
            'content' => "m{$sequence}", 'content_type' => 'text/plain',
            'created_at' => '2026-01-01T00:00:00Z', 'sequence' => $sequence];
    }
}
