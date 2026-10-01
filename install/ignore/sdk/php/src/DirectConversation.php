<?php

declare(strict_types=1);

namespace AirwayIM;

/** A direct (1:1) conversation; the peer of an incoming message is $message->sender. */
final class DirectConversation extends Conversation
{
    public function __construct(Client $client, string $id)
    {
        parent::__construct($client, $id, 'direct');
    }
}
