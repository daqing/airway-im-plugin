<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * A message response that behaves like a JSON object (array access, JSON
 * serialization) while also exposing its fields as property reads:
 * $message->id, $message->sequence, $message->sender->uuid, and so on.
 */
final class Message extends DataObject
{
    public function __get(string $name)
    {
        if ($name === 'sender') {
            $sender = $this->data['sender'] ?? null;
            return new Sender(is_array($sender) ? $sender : []);
        }
        return parent::__get($name);
    }
}
