<?php

declare(strict_types=1);

require __DIR__ . '/../src/autoload.php';

final class TestFailure extends \RuntimeException
{
}

/** Minimal xUnit-style base: a fresh instance per test method, teardown after each. */
abstract class TestCase
{
    public ?TestHttpServer $server = null;

    protected function startServer(array $rules): TestHttpServer
    {
        return $this->server = new TestHttpServer($rules);
    }

    public function teardown(): void
    {
        if ($this->server !== null) {
            $this->server->stop();
            $this->server = null;
        }
    }
}

function assertTrue(bool $condition, string $message = 'expected true'): void
{
    if (!$condition) {
        throw new TestFailure($message);
    }
}

function assertFalse(bool $condition, string $message = 'expected false'): void
{
    assertTrue(!$condition, $message);
}

function assertNull($value, string $message = 'expected null'): void
{
    assertTrue($value === null, $message);
}

function assertNotNull($value, string $message = 'expected non-null'): void
{
    assertTrue($value !== null, $message);
}

function assertSame($expected, $actual, string $message = ''): void
{
    if ($expected !== $actual) {
        $details = 'expected ' . var_export($expected, true) . ', got ' . var_export($actual, true);
        throw new TestFailure($message !== '' ? "{$message} — {$details}" : $details);
    }
}

/** Order-insensitive comparison, like Ruby's assert_equal on Hashes. */
function assertEquals($expected, $actual, string $message = ''): void
{
    assertTrue($expected == $actual, $message !== '' ? $message : 'expected equal values');
}

function assertInstanceOf(string $class, $value, string $message = ''): void
{
    assertTrue($value instanceof $class, $message !== '' ? $message : "expected instance of {$class}");
}

function assertStringContains(string $needle, string $haystack, string $message = ''): void
{
    assertTrue(str_contains($haystack, $needle), $message !== '' ? $message : "expected string to contain \"{$needle}\"");
}

/** Runs the callable, expects it to throw $class; returns the caught exception. */
function assertThrows(callable $fn, string $class): \Throwable
{
    try {
        $fn();
    } catch (\Throwable $e) {
        if ($e instanceof $class) {
            return $e;
        }
        throw new TestFailure("expected {$class}, got " . get_class($e) . ': ' . $e->getMessage());
    }
    throw new TestFailure("expected {$class}, nothing was thrown");
}

/** The backend's {code, data, message} envelope as a JSON string. */
function envelope($data, int $code = 0, ?string $message = null): string
{
    return json_encode(['code' => $code, 'data' => $data, 'message' => $message], JSON_UNESCAPED_SLASHES);
}

/**
 * Minimal HTTP/1.1 stub server for the SDK's tests, mirroring the Ruby
 * suite's TCPServer stub: real sockets, one response per request, one
 * server instance per test. PHP has no threads, so the accept loop runs in
 * a child process (test/server_process.php) driven by a data-driven rule
 * list, records every request it served, and exits when this process
 * closes its stdin.
 *
 * Rules are tried in order; a rule applies when its optional `path` matches
 * the raw request path and it has been used fewer than `max` times
 * (default: unlimited); when nothing matches the server answers 500.
 * Rule actions: `abort` (close without responding — the client sees a
 * transport failure), or `status` + `body` (array bodies are JSON-encoded;
 * `chunked` sends the body with Transfer-Encoding: chunked; `sleepMs`
 * delays the response).
 */
final class TestHttpServer
{
    private $process;
    private $stdin;
    private $stdout;
    private $stderr;
    private string $recordFile;
    private int $port;
    private array $tempFiles = [];

    public function __construct(array $rules = [])
    {
        $configFile = $this->tempFile('aim-cfg-');
        $this->recordFile = $this->tempFile('aim-rec-');
        file_put_contents($configFile, json_encode($rules));

        $command = escapeshellarg(PHP_BINARY) . ' '
            . escapeshellarg(__DIR__ . '/server_process.php') . ' '
            . escapeshellarg($configFile) . ' '
            . escapeshellarg($this->recordFile);
        $pipes = [];
        $this->process = proc_open($command, [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
        if (!is_resource($this->process)) {
            throw new \RuntimeException('failed to start the test HTTP server');
        }
        $this->stdin = $pipes[0];
        $this->stderr = $pipes[2];

        $port = trim((string)fgets($pipes[1], 32));
        if (!ctype_digit($port) || $port === '0') {
            $stderr = stream_get_contents($this->stderr);
            throw new \RuntimeException("the test HTTP server did not start: {$stderr}");
        }
        $this->port = (int)$port;
        // Keep the stdout pipe open: closing it early breaks the child's
        // STDOUT while it is still running (any incidental write, e.g. a
        // displayed PHP warning, would kill it). proc_close reaps the pipes.
        $this->stdout = $pipes[1];
    }

    public function url(): string
    {
        return "http://127.0.0.1:{$this->port}";
    }

    /** Requests served so far: [{method, path, headers, body}, ...]. */
    public function requests(): array
    {
        $lines = file($this->recordFile, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [];
        return array_map(static fn (string $line): array => (array)json_decode($line, true), $lines);
    }

    public function stop(): void
    {
        // proc_close reaps the child and closes any pipes still open.
        foreach ([$this->stdin, $this->stdout, $this->stderr] as $pipe) {
            if (is_resource($pipe)) {
                fclose($pipe);
            }
        }
        proc_close($this->process);
        foreach ($this->tempFiles as $file) {
            @unlink($file);
        }
    }

    private function tempFile(string $prefix): string
    {
        $file = tempnam(sys_get_temp_dir(), $prefix);
        $this->tempFiles[] = $file;
        return $file;
    }
}
