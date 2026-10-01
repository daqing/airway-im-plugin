<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Business codes from the backend's {code, data, message} envelope
 * (install/deps/im/docs/api/openapi.md).
 */
final class ErrorCode
{
    public const INTERNAL = 10000;
    public const INVALID_CREDENTIAL = 10001;
    public const INVALID_REQUEST = 10003;
    public const PERMISSION_DENIED = 10005;
    public const CONVERSATION_NOT_FOUND = 11001;
    public const IDEMPOTENCY_KEY_REUSED = 11002;
}
