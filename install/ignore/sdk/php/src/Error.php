<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * The single exception every SDK client throws.
 *
 * getCode() is the envelope business code (10000-11002); -1 when the body
 * was not an envelope or the failure happened before a response arrived.
 * $status is the HTTP status code; 0 for transport failures (network error,
 * timeout).
 */
final class Error extends \RuntimeException
{
    public int $status;

    public function __construct(int $code, string $message, int $status = 0)
    {
        parent::__construct($message, $code);
        $this->status = $status;
    }

    /** The credential is missing, malformed, revoked, or expired. */
    public function authError(): bool
    {
        return $this->getCode() === ErrorCode::INVALID_CREDENTIAL || $this->status === 401;
    }
}
