<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Internal transport shared by all clients: an HTTP/1.1 client built on
 * stream sockets plus {code, data, message} envelope unwrapping
 * (install/deps/im/docs/api/openapi.md).
 *
 * Uses PHP core functions only — no cURL extension, no allow_url_fopen —
 * and opens a fresh connection per request (Connection: close), which keeps
 * instances safe to share across workers/coroutines at the cost of one TCP
 * handshake per call, which server-side integrations can afford.
 */
final class Http
{
    private string $baseUrl;
    private string $scheme;
    private string $host;
    private int $port;
    private int $timeout;

    public function __construct(string $baseUrl, int $timeout = 15)
    {
        $this->baseUrl = preg_replace('#/+$#', '', $baseUrl);
        $parts = parse_url($this->baseUrl);
        if ($parts === false || !isset($parts['scheme'], $parts['host'])) {
            throw new \InvalidArgumentException("invalid base URL: {$baseUrl}");
        }
        $this->scheme = strtolower($parts['scheme']);
        if ($this->scheme !== 'http' && $this->scheme !== 'https') {
            throw new \InvalidArgumentException("unsupported URL scheme: {$parts['scheme']}");
        }
        $this->host = $parts['host'];
        $this->port = $parts['port'] ?? ($this->scheme === 'https' ? 443 : 80);
        $this->timeout = $timeout;
    }

    public function baseUrl(): string
    {
        return $this->baseUrl;
    }

    /**
     * Perform the request and unwrap the envelope: returns the envelope's
     * data on success, throws Error otherwise.
     */
    public function request(string $method, string $path, array $headers = [], ?array $query = null, $body = null)
    {
        [$status, $payload] = $this->transport($method, $path, $headers, $query, $body);
        $envelope = is_array($payload) && array_key_exists('code', $payload) ? $payload : null;

        if ($status >= 200 && $status < 300 && is_array($envelope) && ($envelope['code'] ?? null) === 0) {
            return $envelope['data'] ?? null;
        }
        if (is_array($envelope) && is_int($envelope['code'] ?? null)) {
            $message = $envelope['message'] ?? null;
            throw new Error($envelope['code'], is_string($message) && $message !== '' ? $message : "HTTP {$status}", $status);
        }
        throw new Error(-1, "HTTP {$status}: unexpected response", $status);
    }

    /**
     * Raw request for endpoints outside the envelope contract (storage).
     * Returns [status, parsed-json-or-string-or-null].
     */
    public function transport(string $method, string $path, array $headers = [], ?array $query = null, $body = null): array
    {
        try {
            [$status, $raw] = $this->perform($this->buildTarget($path, $query), $method, $headers, $body);
            return [$status, self::parseBody($raw)];
        } catch (TransportError $e) {
            throw new Error(-1, $e->getMessage(), 0);
        }
    }

    /** @return array{0: int, 1: ?string} [status, raw body] */
    private function perform(string $target, string $method, array $headers, $body): array
    {
        if ($method !== 'GET' && $method !== 'POST' && $method !== 'DELETE' && $method !== 'PUT') {
            throw new \InvalidArgumentException("unsupported HTTP method: {$method}");
        }

        $wire = $this->serializeRequest($target, $method, $headers, $body);
        $socket = @stream_socket_client(
            ($this->scheme === 'https' ? 'ssl://' : 'tcp://') . $this->host . ':' . $this->port,
            $errno,
            $errstr,
            $this->timeout,
            STREAM_CLIENT_CONNECT
        );
        if ($socket === false) {
            $reason = trim($errstr);
            throw new TransportError($reason !== '' ? $reason : "connect failed (errno {$errno})");
        }

        try {
            stream_set_timeout($socket, $this->timeout);
            $this->writeAll($socket, $wire);
            return $this->readResponse($socket);
        } finally {
            fclose($socket);
        }
    }

    private function serializeRequest(string $target, string $method, array $headers, $body): string
    {
        if (is_array($body)) {
            $headers['Content-Type'] = 'application/json';
            $body = json_encode($body, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        }
        $wire = "{$method} {$target} HTTP/1.1\r\n";
        $wire .= 'Host: ' . $this->hostHeader() . "\r\n";
        $wire .= "Connection: close\r\n";
        foreach ($headers as $name => $value) {
            $wire .= "{$name}: {$value}\r\n";
        }
        if ($body !== null) {
            $wire .= 'Content-Length: ' . strlen($body) . "\r\n";
        }
        return $wire . "\r\n" . $body;
    }

    private function hostHeader(): string
    {
        $default = $this->scheme === 'https' ? 443 : 80;
        return $this->port === $default ? $this->host : "{$this->host}:{$this->port}";
    }

    private function buildTarget(string $path, ?array $query): string
    {
        if ($query === null || $query === []) {
            return $path;
        }
        return $path . '?' . http_build_query($query);
    }

    private function writeAll($socket, string $bytes): void
    {
        $length = strlen($bytes);
        $written = 0;
        while ($written < $length) {
            $n = @fwrite($socket, substr($bytes, $written));
            if ($n === false || $n === 0) {
                throw new TransportError($this->timedOut($socket) ? 'write timed out' : 'connection lost while writing the request');
            }
            $written += $n;
        }
    }

    /** @return array{0: int, 1: ?string} */
    private function readResponse($socket): array
    {
        [$status, $headers, $rest] = $this->readHead($socket);

        // Interim responses (e.g. 100 Continue) are not the response; we
        // never send Expect, but skip them defensively.
        while ($status >= 100 && $status < 200) {
            [$status, $headers, $rest] = $this->readHead($socket, $rest);
        }

        $chunked = str_contains(strtolower($headers['transfer-encoding'] ?? ''), 'chunked');
        if ($chunked) {
            return [$status, self::decodeChunked($this->readToEnd($socket, $rest))];
        }
        if (isset($headers['content-length'])) {
            return [$status, $this->readExact($socket, $rest, (int)$headers['content-length'])];
        }
        return [$status, $this->readToEnd($socket, $rest)];
    }

    /**
     * Read (or continue reading) one header block.
     *
     * @return array{0: int, 1: array<string, string>, 2: string} [status, lower-cased headers, unconsumed bytes]
     */
    private function readHead($socket, string $buffer = ''): array
    {
        while (($headEnd = strpos($buffer, "\r\n\r\n")) === false) {
            $buffer .= $this->readChunk($socket);
        }
        $head = substr($buffer, 0, $headEnd);
        $rest = substr($buffer, $headEnd + 4);
        $lines = explode("\r\n", $head);
        if (!preg_match('#^HTTP/\S+[ \t]+(\d{3})#', $lines[0], $matches)) {
            throw new TransportError('malformed HTTP response');
        }
        $headers = [];
        for ($i = 1; $i < count($lines); $i++) {
            [$name, $value] = explode(':', $lines[$i], 2);
            $headers[strtolower(trim($name))] = trim($value);
        }
        return [(int)$matches[1], $headers, $rest];
    }

    private function readExact($socket, string $buffer, int $length): ?string
    {
        while (strlen($buffer) < $length) {
            $buffer .= $this->readChunk($socket);
        }
        return substr($buffer, 0, $length);
    }

    private function readToEnd($socket, string $buffer): ?string
    {
        while (!feof($socket)) {
            $chunk = @fread($socket, 65536);
            if ($chunk === false || $chunk === '') {
                if ($this->timedOut($socket)) {
                    throw new TransportError('read timed out');
                }
                break; // clean EOF: the normal end for bodies without a declared length
            }
            $buffer .= $chunk;
        }
        return $buffer;
    }

    private function readChunk($socket): string
    {
        $chunk = @fread($socket, 65536);
        if ($chunk === false || $chunk === '') {
            if ($this->timedOut($socket)) {
                throw new TransportError('read timed out');
            }
            throw new TransportError('connection closed before the response completed');
        }
        return $chunk;
    }

    private function timedOut($socket): bool
    {
        return (bool)(stream_get_meta_data($socket)['timed_out'] ?? false);
    }

    private static function decodeChunked(string $data): ?string
    {
        $decoded = '';
        $pos = 0;
        while (true) {
            $lineEnd = strpos($data, "\r\n", $pos);
            if ($lineEnd === false) {
                throw new TransportError('malformed chunked response');
            }
            $size = (int)hexdec(explode(';', trim(substr($data, $pos, $lineEnd - $pos)), 2)[0]);
            if ($size === 0) {
                return $decoded; // terminal chunk; trailers are ignored
            }
            $decoded .= substr($data, $lineEnd + 2, $size);
            $pos = $lineEnd + 2 + $size + 2;
            if ($pos > strlen($data)) {
                throw new TransportError('malformed chunked response');
            }
        }
    }

    private static function parseBody(?string $body)
    {
        if ($body === null || $body === '') {
            return null;
        }
        $decoded = json_decode($body, true);
        return json_last_error() === JSON_ERROR_NONE ? $decoded : $body;
    }
}
