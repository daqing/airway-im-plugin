<?php

declare(strict_types=1);

// End-to-end verification of the SDK against a running backend stack
// (backend :1905, internal :1906 — see the repo root README). The PHP SDK
// is REST-only, so this mirrors the REST phases of the TS suite's
// test/e2e.node.ts: mint dev credentials, then exercise identity, direct +
// group conversations (through the conversation handles), sequence sync,
// idempotent replay, storage upload, and the admin API.
//
//   php test/e2e.php
//
// Env overrides: IM_API_URL, IM_INTERNAL_URL, IM_INTERNAL_SECRET,
// IM_ADMIN_USERNAME, IM_ADMIN_PASSWORD.

require __DIR__ . '/../src/autoload.php';

use AirwayIM\AdminClient;
use AirwayIM\Client;
use AirwayIM\DirectConversation;
use AirwayIM\GroupConversation;
use AirwayIM\Credentials;
use AirwayIM\ErrorCode;
use AirwayIM\Error;
use AirwayIM\InternalClient;

$apiUrl = getenv('IM_API_URL') ?: 'http://127.0.0.1:1905';
$internalUrl = getenv('IM_INTERNAL_URL') ?: 'http://127.0.0.1:1906';
$internalSecret = getenv('IM_INTERNAL_SECRET') ?: 'dev-only-internal-secret';
$adminUser = getenv('IM_ADMIN_USERNAME') ?: 'admin';
$adminPass = getenv('IM_ADMIN_PASSWORD') ?: 'dev-only-admin';

$passed = 0;

function ok(string $label, bool $condition, string $detail = ''): void
{
    global $passed;
    if (!$condition) {
        fwrite(STDERR, "✗ {$label}" . ($detail !== '' ? " — {$detail}" : '') . "\n");
        exit(1);
    }
    $passed++;
    echo "✓ {$label}\n";
}

function uniqueUuid(string $name): string
{
    return $name . '-' . bin2hex(random_bytes(4));
}

echo "e2e against {$apiUrl} (internal {$internalUrl})\n";

// ---- Credential minting (server-to-server) ----
$internal = new InternalClient($internalUrl, $internalSecret);

$aliceUuid = uniqueUuid('sdk-alice');
$bobUuid = uniqueUuid('sdk-bob');
$carolUuid = uniqueUuid('sdk-carol');

$aliceMinted = $internal->mintCredential(uuid: $aliceUuid, name: 'sdk-alice', nickname: 'Alice');
ok('mint credential for alice', $aliceMinted->credential() !== '' && $aliceMinted->expiresAt() !== null);

$bobCred = $internal->mintCredential(uuid: $bobUuid, name: 'sdk-bob')->credential();
$carolCred = $internal->mintCredential(uuid: $carolUuid, name: 'sdk-carol')->credential();
ok('mint credentials for bob and carol', $bobCred !== '' && $carolCred !== '');

// ---- Credentials helpers agree with the minted token ----
$claims = Credentials::decode($aliceMinted->credential());
assertSameValue($claims['uuid'], $aliceUuid, 'decode(credential) recovers the uuid');
assertFalseValue(Credentials::verifySignature($aliceMinted->credential(), 'not-the-signing-secret'), 'signature does not verify under a wrong secret');
assertFalseValue(Credentials::expired($aliceMinted->credential()), 'freshly minted credential is not expired');

// ---- Identity ----
$alice = new Client(apiUrl: $apiUrl, credential: $aliceMinted->credential());
$bob = new Client(apiUrl: $apiUrl, credential: $bobCred);
$carol = new Client(apiUrl: $apiUrl, credential: $carolCred);

$aliceMe = $alice->me();
$bobMe = $bob->me();
ok('me(): profiles resolve', ($aliceMe['uuid'] ?? '') === $aliceUuid && ($bobMe['uuid'] ?? '') === $bobUuid);

// ---- Direct conversation handle + sequence sync ----
$direct = $alice->createDirect($bobUuid);
ok('direct get-or-create returns a DirectConversation handle',
    $direct instanceof DirectConversation
    && $direct->kind === 'direct'
    && strlen($direct->id) === 26);
$same = $alice->createDirect($bobUuid);
ok('direct get-or-create is stable', $same->id === $direct->id);

$readOnly = $alice->getDirect($bobUuid);
ok('getDirect returns the existing handle',
    $readOnly instanceof DirectConversation && $readOnly->id === $direct->id);
assertNullValue($carol->getDirect($bobUuid), 'getDirect returns null when absent');

$sent = $direct->send('hello **bob**', contentType: 'text/markdown');
ok('handle send()', $sent->sender->uuid === $aliceUuid && $sent->sequence === 1);

$polled = $bob->listMessages($direct->id, afterSequence: 0, limit: 100);
ok('recipient polls the message by sequence', ($polled[0]->id ?? null) === $sent->id && ($polled[0]->content ?? null) === 'hello **bob**');

$reply = $bob->sendDirectMessage($aliceUuid, 'hi alice');
ok('sendDirectMessage one-call flow', $reply->sender->uuid === $bobUuid);

$history = [];
foreach ($bob->eachMessage($direct->id) as $message) {
    $history[] = $message->sequence;
}
ok('eachMessage walks the history in ascending order', $history === [1, 2]);

// ---- Idempotent replay ----
$idemKey = 'php-e2e-' . bin2hex(random_bytes(8));
$groupMsg = $direct->send('idempotent body', idempotencyKey: $idemKey, retries: 0);
$replay = $direct->send('idempotent body', idempotencyKey: $idemKey, retries: 0);
ok('same idempotency key replays the original message', $replay->id === $groupMsg->id && $replay->sequence === $groupMsg->sequence);

$key = 'php-e2e-' . bin2hex(random_bytes(8));
$direct->send('original', idempotencyKey: $key, retries: 0);
$reused = null;
try {
    $direct->send('DIFFERENT BODY', idempotencyKey: $key, retries: 0);
} catch (Error $e) {
    $reused = $e;
}
ok('key reuse with different body rejected', $reused !== null && $reused->getCode() === ErrorCode::IDEMPOTENCY_KEY_REUSED, $reused ? "code={$reused->getCode()}" : 'no throw');

// ---- Group conversation handle ----
$group = $alice->createGroup(title: 'PHP SDK E2E Group', memberUUIDs: [$bobUuid, $carolUuid]);
ok('createGroup returns a GroupConversation handle',
    $group instanceof GroupConversation
    && $group->kind === 'group'
    && $group->title === 'PHP SDK E2E Group');

$daveUuid = uniqueUuid('sdk-dave');
$internal->mintCredential(uuid: $daveUuid, name: 'sdk-dave');
$group->addMembers([$daveUuid]);
$details = $group->details();
ok('handle addMembers + details', ($details['type'] ?? '') === 'group' && count($details['members'] ?? []) >= 3);

$groups = $bob->listGroups();
ok('listGroups sees the new group', in_array($group->id, array_column($groups, 'id'), true));

$opened = $bob->openConversation($group->id);
ok('openConversation resolves the group handle',
    $opened instanceof GroupConversation && $opened->id === $group->id);

$groupMessage = $group->send('first group message');
$review = $bob->listMessages($group->id, afterSequence: 0);
ok('group message readable by a member', ($review[0]->id ?? null) === $groupMessage->id);

// ---- Storage upload (development-stage public API) ----
$tmp = tempnam(sys_get_temp_dir(), 'php-sdk-e2e-');
file_put_contents($tmp, "php sdk e2e upload\n");
try {
    $upload = $alice->uploadFile($tmp, filename: 'note.txt', dir: 'sdk-e2e');
} finally {
    @unlink($tmp);
}
ok('uploadFile returns key/url/size', ($upload['key'] ?? '') !== '' && (int)($upload['size'] ?? 0) > 0);
$downloadUrl = $alice->storageUrl((string)$upload['key']);
ok('storageUrl is absolute and keeps the key', str_starts_with($downloadUrl, $apiUrl) && str_contains($downloadUrl, 'storage/'));

// ---- Admin API ----
$admin = new AdminClient(apiUrl: $apiUrl, username: $adminUser, password: $adminPass);
ok('admin status', isset($admin->status()['users']));
$users = $admin->users();
ok('admin users lists the minted identities', in_array($aliceUuid, array_column($users, 'uuid'), true));

$admin->revokeUser($carolUuid);
try {
    $carol->me();
    ok('revoked credential rejected', false);
} catch (Error $e) {
    ok('revoked credential rejected', $e->authError(), "code={$e->getCode()}");
}

// ---- Credential renewal through the host callback ----
$renewed = 0;
$renewable = new Client(
    apiUrl: $apiUrl,
    credential: 'im1.stale.token',
    getCredential: function () use ($internal, $aliceUuid, &$renewed): string {
        $renewed++;
        return $internal->mintCredential(uuid: $aliceUuid, name: 'sdk-alice')->credential();
    }
);
ok('getCredential callback recovers from a stale credential', ($renewable->me()['uuid'] ?? '') === $aliceUuid && $renewed === 1);

echo "\nall {$passed} checks passed\n";

function assertTrueValue(bool $condition, string $label): void
{
    ok($label, $condition);
}

function assertFalseValue(bool $condition, string $label): void
{
    ok($label, !$condition);
}

function assertNullValue($value, string $label): void
{
    ok($label, $value === null);
}

function assertSameValue($expected, $actual, string $label): void
{
    ok($label, $expected === $actual);
}
