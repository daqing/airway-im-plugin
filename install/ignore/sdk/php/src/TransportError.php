<?php

declare(strict_types=1);

namespace AirwayIM;

/** @internal Transport-level failure, mapped by Http to Error(code -1, status 0). */
final class TransportError extends \RuntimeException
{
}
