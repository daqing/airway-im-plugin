<?php

declare(strict_types=1);

namespace AirwayIM;

/** @internal */
final class Util
{
    /**
     * Identifiers are opaque strings; escape them so "/" or "?" in a value
     * can never change the request path. Path-segment escaping: a space
     * becomes %20 (form encoding's "+" would change the value in a path).
     * rawurlencode encodes exactly the RFC 3986 unreserved complement, byte
     * by byte.
     */
    public static function escapeSegment(string $value): string
    {
        return rawurlencode($value);
    }
}
