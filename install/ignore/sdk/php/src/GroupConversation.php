<?php

declare(strict_types=1);

namespace AirwayIM;

/**
 * A group conversation; carries the group-only operations. $title is the
 * creation-time title (null when created without one or when opened by id);
 * details() returns the live value.
 */
final class GroupConversation extends Conversation
{
    public function __construct(Client $client, string $id, public ?string $title = null)
    {
        parent::__construct($client, $id, 'group');
    }

    /** Add members by uuid (owner/admin; idempotent for already-active members). */
    public function addMembers(array $memberUUIDs): array
    {
        return $this->client->addMembers($this->id, $memberUUIDs);
    }

    /** Remove members by uuid (owner/admin; cannot remove self or the owner). */
    public function removeMembers(array $userUUIDs): array
    {
        return $this->client->removeMembers($this->id, $userUUIDs);
    }
}
