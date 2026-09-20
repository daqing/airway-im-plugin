<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * Author profile inside a Message, same JSON-object-with-property-reads
 * contract: $message->sender->uuid or $message["sender"]["uuid"].
 */
final class Sender extends DataObject
{
}
