<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Base for API responses that behave like a JSON object (array access,
 * JSON serialization, unknown-field preservation) while also exposing their
 * fields as property reads — $message["id"], $message->id, and
 * $message->sender->uuid all work.
 */
abstract class DataObject implements \ArrayAccess, \JsonSerializable
{
    protected array $data;

    public function __construct(array $data = [])
    {
        $this->data = $data;
    }

    public function toArray(): array
    {
        return $this->data;
    }

    public function __get(string $name)
    {
        return $this->data[$name] ?? null;
    }

    public function __isset(string $name): bool
    {
        return isset($this->data[$name]);
    }

    public function jsonSerialize(): array
    {
        return $this->data;
    }

    public function offsetExists(mixed $offset): bool
    {
        return isset($this->data[$offset]);
    }

    public function offsetGet(mixed $offset): mixed
    {
        return $this->data[$offset] ?? null;
    }

    public function offsetSet(mixed $offset, mixed $value): void
    {
        $this->data[$offset] = $value;
    }

    public function offsetUnset(mixed $offset): void
    {
        unset($this->data[$offset]);
    }
}
