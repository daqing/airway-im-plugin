<?php

declare(strict_types=1);

use AirwayIM\Credentials;
use AirwayIM\Error;

final class CredentialsTest extends TestCase
{
    private const SECRET = 'test-secret';

    // Cross-language regression vector generated with the Node.js reference
    // implementation from install/deps/im/docs/design/identity.md §4.2:
    // claims {uuid: "user-42", name: "alice", iat: 1700000000, exp: 1700086400}.
    private const NODE_VECTOR = 'im1.eyJ1dWlkIjoidXNlci00MiIsIm5hbWUiOiJhbGljZSIsImlhdCI6MTcwMDAwMDAwMCwiZXhwIjoxNzAwMDg2NDAwfQ.w_i4UIKCVjBY0XwnDdeRsnOCHoqgxxl1rDAESDh7VeU';

    public function testSignMatchesNodeReferenceVectorByteForByte(): void
    {
        $credential = Credentials::sign(
            secret: self::SECRET,
            uuid: 'user-42',
            name: 'alice',
            now: 1700000000,
            ttl: 86400
        );
        assertSame(self::NODE_VECTOR, $credential);
    }

    public function testSignIncludesOptionalFieldsWhenPresent(): void
    {
        $credential = Credentials::sign(
            secret: self::SECRET,
            uuid: 'u1',
            name: 'bob',
            nickname: 'Bob',
            avatarUrl: 'https://example.com/bob.png',
            tokenVersion: 3,
            now: 1700000000,
            ttl: 60
        );
        $claims = Credentials::decode($credential);
        assertEquals(
            ['uuid' => 'u1', 'name' => 'bob', 'nickname' => 'Bob',
                'avatar_url' => 'https://example.com/bob.png',
                'token_version' => 3, 'iat' => 1700000000,
                'exp' => 1700000060],
            $claims
        );
    }

    public function testSignOmitsNullOptionalFields(): void
    {
        $credential = Credentials::sign(secret: self::SECRET, uuid: 'u1', name: 'bob', now: 0);
        assertEquals(['uuid' => 'u1', 'name' => 'bob', 'iat' => 0, 'exp' => 86400], Credentials::decode($credential));
    }

    public function testSignOmitsExpWhenTtlIsNull(): void
    {
        $credential = Credentials::sign(secret: self::SECRET, uuid: 'u1', name: 'bob', ttl: null, now: 0);
        assertNull(Credentials::decode($credential)['exp'] ?? null);
        assertTrue(Credentials::verifySignature($credential, self::SECRET));
    }

    public function testSignRejectsOutOfRangeFields(): void
    {
        assertThrows(fn () => Credentials::sign(secret: self::SECRET, uuid: str_repeat('x', 65), name: 'bob'), \InvalidArgumentException::class);
        assertThrows(fn () => Credentials::sign(secret: self::SECRET, uuid: 'u1', name: ''), \InvalidArgumentException::class);
        assertThrows(fn () => Credentials::sign(secret: self::SECRET, uuid: 'u1', name: 'bob', nickname: str_repeat('x', 65)), \InvalidArgumentException::class);
        assertThrows(fn () => Credentials::sign(secret: self::SECRET, uuid: 'u1', name: 'bob', avatarUrl: str_repeat('x', 2049)), \InvalidArgumentException::class);
        assertThrows(fn () => Credentials::sign(secret: self::SECRET, uuid: 'u1', name: 'bob', tokenVersion: 0), \InvalidArgumentException::class);
    }

    public function testDecodeRoundTrip(): void
    {
        $claims = ['uuid' => 'user-42', 'name' => 'alice', 'iat' => 1700000000, 'exp' => 1700086400];
        assertEquals($claims, Credentials::decode(self::NODE_VECTOR));
    }

    public function testDecodeRejectsMalformedInput(): void
    {
        foreach (['', 'not-a-credential', 'im2.aaa.bbb', 'im1.aaa', 'im1.aaa.bbb.ccc'] as $bad) {
            assertThrows(fn () => Credentials::decode($bad), Error::class);
        }
    }

    public function testVerifySignatureAcceptsValidAndRejectsTampered(): void
    {
        assertTrue(Credentials::verifySignature(self::NODE_VECTOR, self::SECRET));
        assertFalse(Credentials::verifySignature(self::NODE_VECTOR, 'wrong-secret'));

        $parts = explode('.', self::NODE_VECTOR);
        $parts[1] = ($parts[1][0] === 'A' ? 'B' : 'A') . substr($parts[1], 1);
        assertFalse(Credentials::verifySignature(implode('.', $parts), self::SECRET));
        assertFalse(Credentials::verifySignature('', self::SECRET));
        assertFalse(Credentials::verifySignature('garbage', self::SECRET));
    }

    public function testExpired(): void
    {
        assertFalse(Credentials::expired(self::NODE_VECTOR, now: 1700086399));
        assertTrue(Credentials::expired(self::NODE_VECTOR, now: 1700086400));

        $never = Credentials::sign(secret: self::SECRET, uuid: 'u1', name: 'bob', ttl: null, now: 0);
        assertFalse(Credentials::expired($never, now: 4102444800));
    }
}
