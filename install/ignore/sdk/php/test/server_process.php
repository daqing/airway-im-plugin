<?php

declare(strict_types=1);

// Child process behind TestHttpServer (see test_helper.php): binds
// 127.0.0.1:0, prints the chosen port, serves requests according to the
// rule list until stdin closes, recording every request along the way.

ini_set('display_errors', 'stderr'); // never write to STDOUT: the port line is the only thing on it

$configFile = $argv[1] ?? exit(1);
$recordFile = $argv[2] ?? exit(1);

$rules = json_decode((string)file_get_contents($configFile), true) ?: [];

$server = stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
if ($server === false) {
    fwrite(STDERR, "bind failed: {$errstr} ({$errno})");
    exit(1);
}
$name = (string)stream_socket_get_name($server, false);
fwrite(STDOUT, substr($name, strrpos($name, ':') + 1) . "\n");
fflush(STDOUT);

stream_set_blocking($server, false);
stream_set_blocking(STDIN, false);
$uses = [];

while (true) {
    $read = [$server, STDIN];
    $write = null;
    $except = null;
    if (stream_select($read, $write, $except, null) === false) {
        break;
    }
    if (in_array(STDIN, $read, true)) {
        $data = fread(STDIN, 1024);
        if ($data === false || $data === '' || feof(STDIN)) {
            break; // parent closed the pipe: shut down gracefully
        }
    }
    if (in_array($server, $read, true)) {
        $client = @stream_socket_accept($server, 1);
        if ($client !== false) {
            handleConnection($client, $rules, $recordFile, $uses);
        }
    }
}

exit(0);

function handleConnection($client, array $rules, string $recordFile, array &$uses): void
{
    try {
        $request = readRequest($client);
        if ($request === null) {
            return;
        }
        file_put_contents($recordFile, json_encode($request, JSON_UNESCAPED_SLASHES) . "\n", FILE_APPEND);

        $rule = pickRule($rules, $request['path'], $uses);
        if ($rule === null) {
            respond($client, 500, json_encode(['error' => 'no matching rule']));
            return;
        }
        if (!empty($rule['sleepMs'])) {
            usleep((int)$rule['sleepMs'] * 1000);
        }
        if (!empty($rule['abort'])) {
            return; // close without responding: the client sees a transport failure
        }
        $body = $rule['body'] ?? '';
        if (!is_string($body)) {
            $body = json_encode($body, JSON_UNESCAPED_SLASHES);
        }
        respond($client, (int)($rule['status'] ?? 200), $body, !empty($rule['chunked']));
    } finally {
        fclose($client);
    }
}

function pickRule(array $rules, string $path, array &$uses): ?array
{
    foreach ($rules as $index => $rule) {
        if (($uses[$index] ?? 0) >= ($rule['max'] ?? PHP_INT_MAX)) {
            continue;
        }
        if (isset($rule['path']) && $rule['path'] !== $path) {
            continue;
        }
        $uses[$index] = ($uses[$index] ?? 0) + 1;
        return $rule;
    }
    return null;
}

/** @return array{method: string, path: string, headers: array, body: string}|null */
function readRequest($client): ?array
{
    stream_set_blocking($client, true);
    $head = '';
    while (($headEnd = strpos($head, "\r\n\r\n")) === false) {
        $chunk = fread($client, 65536);
        if ($chunk === false || $chunk === '') {
            return null;
        }
        $head .= $chunk;
    }
    [$headPart, $bodyPart] = explode("\r\n\r\n", $head, 2);
    $lines = explode("\r\n", $headPart);
    $parts = preg_split('/\s+/', $lines[0]);
    $headers = [];
    for ($i = 1; $i < count($lines); $i++) {
        [$name, $value] = explode(':', $lines[$i], 2);
        $headers[strtolower(trim($name))] = trim($value);
    }
    $length = (int)($headers['content-length'] ?? 0);
    while (strlen($bodyPart) < $length) {
        $chunk = fread($client, 65536);
        if ($chunk === false || $chunk === '') {
            break;
        }
        $bodyPart .= $chunk;
    }
    return [
        'method' => $parts[0] ?? '',
        'path' => $parts[1] ?? '',
        'headers' => $headers,
        'body' => substr($bodyPart, 0, $length),
    ];
}

function respond($client, int $status, string $body, bool $chunked = false): void
{
    $reasons = [
        200 => 'OK', 201 => 'Created', 400 => 'Bad Request', 401 => 'Unauthorized',
        403 => 'Forbidden', 404 => 'Not Found', 409 => 'Conflict', 500 => 'Internal Server Error',
    ];
    $reason = $reasons[$status] ?? 'Status';
    $head = "HTTP/1.1 {$status} {$reason}\r\nContent-Type: application/json\r\n";
    if ($chunked) {
        $head .= "Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n";
        fwrite($client, $head);
        $half = (int)ceil(strlen($body) / 2);
        foreach ([substr($body, 0, $half), substr($body, $half)] as $piece) {
            if ($piece === '') {
                continue;
            }
            fwrite($client, dechex(strlen($piece)) . "\r\n{$piece}\r\n");
        }
        fwrite($client, "0\r\n\r\n");
        return;
    }
    $head .= 'Content-Length: ' . strlen($body) . "\r\nConnection: close\r\n\r\n";
    fwrite($client, $head . $body);
}
