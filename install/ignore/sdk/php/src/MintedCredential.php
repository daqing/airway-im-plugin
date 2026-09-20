<?php

declare(strict_types=1);

namespace AirwayIM;

/** A freshly minted credential. expiresAt is null for ttlSeconds 0 (no expiry claim — service credentials only). */
final class MintedCredential
{
    public function __construct(
        private string $credential,
        private ?string $expiresAt
    ) {
    }

    public function credential(): string
    {
        return $this->credential;
    }

    public function expiresAt(): ?string
    {
        return $this->expiresAt;
    }
}
