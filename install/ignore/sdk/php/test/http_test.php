<?php

declare(strict_types=1);

use AirwayIM\Http;
use AirwayIM\Error;

/** Direct coverage of the stream-socket transport behind the envelope layer. */
final class HttpTest extends TestCase
{
    public function testTransportParsesJsonAndPassesThroughRawStrings(): void
    {
        $this->startServer([
            ['path' => '/json', 'status' => 200, 'body' => ['code' => 0, 'data' => ['x' => 1], 'message' => null]],
            ['path' => '/raw', 'status' => 200, 'body' => 'plain text, not json'],
        ]);
        $http = new Http($this->server->url() . '/', 5);

        [$status, $payload] = $http->transport('GET', '/json');
        assertSame(200, $status);
        assertEquals(['code' => 0, 'data' => ['x' => 1], 'message' => null], $payload);

        [$status, $payload] = $http->transport('GET', '/raw');
        assertSame(200, $status);
        assertSame('plain text, not json', $payload);
    }

    public function testDecodesChunkedResponses(): void
    {
        $this->startServer([
            ['path' => '/chunked', 'status' => 200, 'body' => envelope(['ok' => true]), 'chunked' => true],
        ]);
        $http = new Http($this->server->url(), 5);

        [$status, $payload] = $http->transport('GET', '/chunked');
        assertSame(200, $status);
        assertEquals(['code' => 0, 'data' => ['ok' => true], 'message' => null], $payload);
    }

    public function testSendsRequestBodyWithJsonContentType(): void
    {
        $this->startServer([['status' => 200, 'body' => envelope(null)]]);
        $http = new Http($this->server->url(), 5);

        $http->request('POST', '/things', [], null, ['a' => 1]);

        $request = $this->server->requests()[0];
        assertSame('application/json', $request['headers']['content-type']);
        assertSame('7', $request['headers']['content-length']); // {"a":1}
        assertEquals(['a' => 1], json_decode($request['body'], true));
    }

    public function testRequestTargetCarriesHostAndPort(): void
    {
        $this->startServer([['status' => 200, 'body' => envelope(null)]]);
        $http = new Http($this->server->url(), 5);

        $http->request('GET', '/x');

        assertSame('127.0.0.1:' . parse_url($this->server->url(), PHP_URL_PORT), $this->server->requests()[0]['headers']['host']);
    }

    public function testConnectionRefusedMapsToTransportError(): void
    {
        // Port 1 on loopback: nothing listens there, the connect fails fast.
        $http = new Http('http://127.0.0.1:1', 2);

        $error = assertThrows(fn () => $http->transport('GET', '/'), Error::class);
        assertSame(-1, $error->getCode());
        assertSame(0, $error->status);
    }

    public function testReadTimeoutMapsToTransportError(): void
    {
        $this->startServer([['status' => 200, 'body' => envelope(null), 'sleepMs' => 3000]]);
        $http = new Http($this->server->url(), 1);

        $error = assertThrows(fn () => $http->transport('GET', '/'), Error::class);
        assertSame(-1, $error->getCode());
        assertSame(0, $error->status);
        assertSame('read timed out', $error->getMessage());
    }

    public function testUnsupportedMethodIsRejectedBeforeAnyIo(): void
    {
        $http = new Http('http://127.0.0.1:1', 2);

        assertThrows(fn () => $http->transport('PATCH', '/'), \InvalidArgumentException::class);
    }

    public function testInvalidBaseUrlIsRejected(): void
    {
        assertThrows(fn () => new Http('not-a-url'), \InvalidArgumentException::class);
        assertThrows(fn () => new Http('ftp://example.com'), \InvalidArgumentException::class);
    }
}
