# Airway IM Backend OpenAPI Reference

This document is the client integration contract for the HTTP endpoints that
are currently implemented by the Airway IM backend. The YAML block is a valid
OpenAPI 3.1 document and can be copied into an `.yaml` file for code generation.

Implementation notes:

- Production clients must use HTTPS. The HTTP server URL is for local testing.
- Authenticated API operations use an Airway-signed user credential:
  `Authorization: Bearer <user-credential>`. The signing backend is the
  integrating platform's own server (see identity.md §4.1) — never the
  IM API itself, and never the client. It signs the credential with the
  shared `IM_AUTH_SECRET`, or obtains one on the user's behalf from
  `POST /internal/v1/credentials`. The first successful authentication
  registers the user automatically; there is no separate provisioning
  step. See [`../design/identity.md`](../design/identity.md).
- Application JSON responses generally use `{code, data, message}`. Storage
  endpoints predate that envelope and intentionally document their actual
  response shapes.
- Storage endpoints are currently public in the implementation. A macOS client
  must not assume that possession of a storage key is a permanent authorization
  model; authentication will be added before production use.
- WebSocket delivery is described after the OpenAPI document because WebSocket
  frames are outside the OpenAPI HTTP operation model.

```yaml
openapi: 3.1.0
info:
  title: Airway IM Backend API
  version: 0.1.0
  description: >-
    Implemented HTTP API for Airway IM clients. Messages are created over HTTP;
    the Gateway WebSocket is used only for authenticated server-to-client events.
servers:
  - url: https://api.example.invalid
    description: Production placeholder
  - url: http://127.0.0.1:1905
    description: Local development
tags:
  - name: System
  - name: Identity
  - name: Conversations
  - name: Messages
  - name: Admin
  - name: Storage

paths:
  /health:
    get:
      tags: [System]
      operationId: getHealth
      summary: Check API process health
      security: []
      responses:
        '200':
          description: The API process is running.
          content:
            text/plain:
              schema:
                type: string
                const: "UP\n"
              example: "UP\n"

  /api/v1/me:
    get:
      tags: [Identity]
      operationId: getCurrentUser
      summary: Get the authenticated user
      security:
        - bearerAuth: []
      responses:
        '200':
          description: Authenticated user profile.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/UserEnvelope'
        '400':
          $ref: '#/components/responses/InvalidBearerToken'
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/conversations:
    get:
      tags: [Conversations]
      operationId: listConversations
      summary: List the authenticated user's group conversations
      description: >-
        Returns group conversations where the authenticated user is an active
        member. Direct conversations, groups belonging only to other users, and
        groups the user has left are excluded. Results are ordered by updated_at
        descending, with conversation ID descending as a stable tie-breaker.
      security:
        - bearerAuth: []
      parameters:
        - name: type
          in: query
          required: true
          description: Conversation type to return. Only group is currently supported.
          schema:
            type: string
            enum: [group]
          example: group
      responses:
        '200':
          description: >-
            Active group conversations. data is an empty array when the user
            does not have any groups.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationListEnvelope'
              example:
                code: 0
                data:
                  - id: 01J2Q7D4N5R8TK6VD3SZ1H0Y9M
                    kind: group
                    title: Airway IM Backend
                    avatar_url: null
                    created_by: 1
                    created_at: '2026-07-24T08:00:00Z'
                    updated_at: '2026-07-24T09:30:00Z'
                message: null
        '400':
          description: Invalid or expired credential, or unsupported/missing conversation type.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '500':
          $ref: '#/components/responses/InternalError'

    post:
      tags: [Conversations]
      operationId: createConversation
      summary: Create or resolve a conversation
      description: >-
        A direct request is get-or-create and can return 200 when the canonical
        conversation already exists. Every group request creates a new group.
        The authenticated creator must not be included in member_uuids.
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreateConversationRequest'
            examples:
              direct:
                value:
                  kind: direct
                  member_uuids: [user-2]
              group:
                value:
                  kind: group
                  title: Airway IM Backend
                  member_uuids: [user-2, user-3]
      responses:
        '200':
          description: Existing canonical direct conversation.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationEnvelope'
        '201':
          description: Conversation created.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationEnvelope'
        '400':
          description: Invalid or expired credential, JSON body, conversation kind, or members.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/group:
    post:
      tags: [Conversations]
      operationId: createGroup
      summary: Create a group conversation
      description: >-
        Creates a new group on every successful request. The authenticated user
        becomes the owner. member_uuids identifies the other users to add as
        members by their stable uuid; duplicate uuids and the authenticated
        user's own uuid are removed. After normalization, at least one other
        existing user is required.
      security:
        - bearerAuth: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreateGroupRequest'
            example:
              title: Airway IM Backend
              member_uuids: [user-2, user-3]
      responses:
        '201':
          description: Group conversation created.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationEnvelope'
        '400':
          description: >-
            Invalid or expired credential, JSON body, unknown request field,
            empty normalized member list, or one or more members do not exist.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/conversations/{conversation_uuid}:
    get:
      tags: [Conversations]
      operationId: getConversation
      summary: Get a conversation's type and active members
      description: >-
        Returns the conversation type and current active members when the
        authenticated user is also an active member. Former members are excluded.
        Members are ordered by role (owner, admin, member), join time, and user ID.
      security:
        - bearerAuth: []
      parameters:
        - name: conversation_uuid
          in: path
          required: true
          description: Opaque conversation identifier.
          schema:
            $ref: '#/components/schemas/ULID'
      responses:
        '200':
          description: Conversation type and active member list.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationDetailsEnvelope'
              example:
                code: 0
                data:
                  conversation_uuid: 01J2Q7D4N5R8TK6VD3SZ1H0Y9M
                  type: group
                  members:
                    - id: 1
                      uuid: user-1
                      username: owner
                      nickname: Owner
                      avatar_url: https://avatars.example.com/owner.png
                      role: owner
                    - id: 2
                      uuid: user-2
                      username: member
                      nickname: null
                      avatar_url: null
                      role: member
                message: null
        '400':
          description: Invalid or expired credential, or malformed conversation UUID.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '404':
          description: >-
            Conversation does not exist or the authenticated user is not an
            active member.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/conversations/{conversation_uuid}/members:
    post:
      tags: [Conversations]
      operationId: addConversationMembers
      summary: Add members to a group conversation
      description: >-
        Adds users to a group conversation. Only an active owner or admin may
        add members; direct conversations are rejected. Already-active members
        are skipped, so the operation is idempotent. A former member rejoins
        with left_at cleared, joined_at refreshed, and the base member role.
        When at least one member was added, a conversation.member_added outbox
        event fans out to every active member, including the ones just added.
      security:
        - bearerAuth: []
      parameters:
        - name: conversation_uuid
          in: path
          required: true
          description: Opaque conversation identifier.
          schema:
            $ref: '#/components/schemas/ULID'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/AddMembersRequest'
            example:
              member_uuids: [user-4, user-5]
      responses:
        '200':
          description: Updated conversation details with the active member list.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationDetailsEnvelope'
        '400':
          description: >-
            Invalid or expired credential, malformed conversation UUID, JSON
            body, empty normalized member list, non-group conversation, or one
            or more members do not exist.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '403':
          description: The caller is an active member but not an owner or admin.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '404':
          description: >-
            Conversation does not exist or the caller is not an active member.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/conversations/{conversation_uuid}/members/{user_id}:
    delete:
      tags: [Conversations]
      operationId: removeConversationMember
      summary: Remove a member from a group conversation
      description: >-
        Removes a user from a group conversation by setting left_at, preserving
        membership history. Only an active owner or admin may remove members;
        an admin may only remove plain members, and the owner can never be
        removed (ownership transfer is a separate operation). Callers cannot
        remove themselves. Removing a user who is not an active member is an
        idempotent no-op. On an actual removal, a conversation.member_removed
        outbox event fans out to every active member plus the removed user.
      security:
        - bearerAuth: []
      parameters:
        - name: conversation_uuid
          in: path
          required: true
          description: Opaque conversation identifier.
          schema:
            $ref: '#/components/schemas/ULID'
        - name: user_uuid
          in: path
          required: true
          description: Stable uuid of the user to remove.
          schema:
            type: string
            minLength: 1
            maxLength: 64
      responses:
        '200':
          description: Updated conversation details with the active member list.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ConversationDetailsEnvelope'
        '400':
          description: >-
            Invalid or expired credential, malformed conversation UUID or user
            UUID, non-group conversation, self-removal, or the target is the
            group owner.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '403':
          description: >-
            The caller is an active member but lacks the required role, or an
            admin tried to remove another admin.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '404':
          description: >-
            Conversation does not exist or the caller is not an active member.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/conversations/{conversation_id}/messages:
    get:
      tags: [Messages]
      operationId: listConversationMessages
      summary: Get ordered messages after a sequence
      description: >-
        Returns messages in ascending per-conversation sequence order. Use this
        endpoint after reconnecting or when a WebSocket sequence gap is found.
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/ConversationId'
        - name: after_sequence
          in: query
          description: Return messages whose sequence is greater than this value.
          schema:
            type: integer
            format: int64
            minimum: 0
            default: 0
        - name: limit
          in: query
          description: Values outside 1 through 200 fall back to 100.
          schema:
            type: integer
            minimum: 1
            maximum: 200
            default: 100
      responses:
        '200':
          description: Ordered message page; an empty page is an empty array.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/MessageListEnvelope'
        '400':
          $ref: '#/components/responses/InvalidBearerToken'
        '403':
          description: User is not an active conversation member.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
              example:
                code: 10005
                data: null
                message: Permission denied

    post:
      tags: [Messages]
      operationId: createConversationMessage
      summary: Create a message in a conversation
      description: >-
        Creates the message and its delivery outbox event in one transaction.
        The destination conversation comes from the path. content_type defaults
        to text/markdown. CRLF and CR line endings are normalized to LF. Unknown
        JSON fields and trailing JSON are rejected.
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/ConversationId'
        - name: Idempotency-Key
          in: header
          required: false
          description: >-
            Client-generated key scoped to the authenticated user. Reuse the
            same key only when retrying the same logical request.
          schema:
            type: string
            minLength: 1
            maxLength: 128
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreateConversationMessageRequest'
            example:
              content: Hello **Airway IM**!
              content_type: text/markdown
      responses:
        '200':
          description: Idempotent replay; returns the original message.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/MessageEnvelope'
        '201':
          description: Message and outbox event committed.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/MessageEnvelope'
        '400':
          description: >-
            Invalid or expired credential, conversation ID, JSON, fields,
            content, or idempotency key.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '403':
          description: User is not an active conversation member.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '404':
          description: Conversation disappeared before sequence allocation.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '409':
          description: Idempotency key reused with a different normalized request.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
              example:
                code: 11002
                data: null
                message: Idempotency key was reused with a different request
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/messages:
    post:
      tags: [Messages]
      operationId: createMessage
      summary: Create a durable chat message
      description: >-
        Creates the message and its delivery outbox event in one transaction.
        content_type defaults to text/markdown. CRLF and CR line endings are
        normalized to LF. Unknown JSON fields and trailing JSON are rejected.
      security:
        - bearerAuth: []
      parameters:
        - name: Idempotency-Key
          in: header
          required: false
          description: >-
            Client-generated key scoped to the authenticated user. Reuse the
            same key only when retrying the same logical request.
          schema:
            type: string
            minLength: 1
            maxLength: 128
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreateMessageRequest'
            example:
              conversation_id: 01J2Q7D4N5R8TK6VD3SZ1H0Y9M
              content: Hello **Airway IM**!
              content_type: text/markdown
      responses:
        '200':
          description: Idempotent replay; returns the original message.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/MessageEnvelope'
        '201':
          description: Message and outbox event committed.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/MessageEnvelope'
        '400':
          description: Invalid or expired credential, JSON, fields, content, or idempotency key.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '403':
          description: User is not an active conversation member.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '404':
          description: Conversation disappeared before sequence allocation.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
        '409':
          description: Idempotency key reused with a different normalized request.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorEnvelope'
              example:
                code: 11002
                data: null
                message: Idempotency key was reused with a different request
        '500':
          $ref: '#/components/responses/InternalError'

  /admin/api/login:
    post:
      tags: [Admin]
      operationId: adminLogin
      summary: Log in as administrator
      description: >-
        Exchanges IM_ADMIN_USERNAME / IM_ADMIN_PASSWORD credentials for a
        12-hour administrator session token. This plugin ships no user login
        flow; end users authenticate with Airway-signed credentials (see
        deps/im/docs/design/identity.md).
      security: []
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [username, password]
              properties:
                username: { type: string }
                password: { type: string }
      responses:
        '200':
          description: Session created.
          content:
            application/json:
              schema:
                type: object
                required: [code, data, message]
                properties:
                  code: { type: integer, const: 0 }
                  data:
                    type: object
                    required: [token, expires_at, username]
                    properties:
                      token: { type: string }
                      expires_at: { type: string, format: date-time }
                      username: { type: string }
                  message: { type: 'null' }
        '401':
          description: Invalid administrator credentials.
        '503':
          description: Admin login is not configured.

  /admin/api/users:
    get:
      tags: [Admin]
      operationId: adminListUsers
      summary: List registered users
      description: >-
        Lists identities registered automatically the first time an Airway-signed
        credential authenticated. last_seen_at shows the most recent
        authenticated activity at five-minute granularity.
      security:
        - adminBearerAuth: []
      responses:
        '200':
          description: Users ordered by creation time, newest first.
          content:
            application/json:
              schema:
                type: object
                required: [code, data, message]
                properties:
                  code: { type: integer, const: 0 }
                  data:
                    type: array
                    items:
                      $ref: '#/components/schemas/AdminUser'
                  message: { type: 'null' }
        '401':
          description: Administrator authentication required.
        '500':
          $ref: '#/components/responses/InternalError'

  /admin/api/users/{uuid}/revoke:
    post:
      tags: [Admin]
      operationId: adminRevokeUserCredentials
      summary: Revoke a user's credentials
      description: >-
        Bumps the user's token_version, immediately invalidating every
        credential minted for them by POST /internal/v1/credentials, and
        best-effort kicks their live gateway connections. Revocation is not
        a ban: the Airway project may mint a fresh credential at any time.
      security:
        - adminBearerAuth: []
      parameters:
        - name: uuid
          in: path
          required: true
          schema: { type: string }
      responses:
        '200':
          description: Credentials revoked.
          content:
            application/json:
              schema:
                type: object
                required: [code, data, message]
                properties:
                  code: { type: integer, const: 0 }
                  data:
                    type: object
                    required: [uuid, token_version, connections_kicked]
                    properties:
                      uuid: { type: string }
                      token_version:
                        type: integer
                        format: int64
                        description: The new revocation counter value.
                      connections_kicked:
                        type: integer
                        description: Live gateway connections dropped (0 when the gateway is unreachable).
                  message: { type: 'null' }
        '401':
          description: Administrator authentication required.
        '404':
          description: No registered user with that uuid.
        '500':
          $ref: '#/components/responses/InternalError'

  /admin/api/conversations:
    get:
      tags: [Admin]
      operationId: adminListGroupConversations
      summary: List all group conversations for administrators
      security:
        - adminBearerAuth: []
      responses:
        '200':
          description: Group conversations ordered by latest update.
          content:
            application/json:
              schema:
                type: object
                required: [code, data, message]
                properties:
                  code: { type: integer, const: 0 }
                  data:
                    type: array
                    items:
                      $ref: '#/components/schemas/AdminConversation'
                  message: { type: 'null' }
        '401':
          description: Administrator authentication required.
        '500':
          $ref: '#/components/responses/InternalError'

  /admin/api/conversations/{conversation_id}/messages:
    get:
      tags: [Admin]
      operationId: adminListGroupConversationMessages
      summary: List all messages in a group conversation
      security:
        - adminBearerAuth: []
      parameters:
        - $ref: '#/components/parameters/ConversationId'
      responses:
        '200':
          description: Messages ordered by ascending conversation sequence.
          content:
            application/json:
              schema:
                type: object
                required: [code, data, message]
                properties:
                  code: { type: integer, const: 0 }
                  data:
                    type: array
                    items:
                      $ref: '#/components/schemas/AdminMessage'
                  message: { type: 'null' }
        '401':
          description: Administrator authentication required.
        '404':
          description: Group conversation not found.
        '500':
          $ref: '#/components/responses/InternalError'

  /admin/api/messages/{message_id}/mark-illegal:
    post:
      tags: [Admin]
      operationId: adminMarkMessageIllegal
      summary: Mark a message as illegal
      description: >-
        Persists the moderation state and emits a message.moderated event.
        Administrator responses retain the original content, while client
        message APIs return three asterisks for an illegal message.
      security:
        - adminBearerAuth: []
      parameters:
        - name: message_id
          in: path
          required: true
          schema:
            $ref: '#/components/schemas/ULID'
      responses:
        '200':
          description: The message is marked illegal; repeated requests are idempotent.
          content:
            application/json:
              schema:
                type: object
                required: [code, data, message]
                properties:
                  code: { type: integer, const: 0 }
                  data:
                    $ref: '#/components/schemas/AdminMessage'
                  message: { type: 'null' }
        '401':
          description: Administrator authentication required.
        '404':
          description: Group message not found.
        '500':
          $ref: '#/components/responses/InternalError'

  /api/v1/storage:
    post:
      tags: [Storage]
      operationId: uploadFile
      summary: Upload one file
      description: >-
        The backend generates a key shaped as
        <dir>/<yyyymm>/<random-hex><extension>. This operation currently has no
        authentication middleware and must be treated as development-stage API.
      security: []
      requestBody:
        required: true
        content:
          multipart/form-data:
            schema:
              type: object
              required: [file]
              properties:
                file:
                  type: string
                  format: binary
                dir:
                  type: string
                  description: Optional key prefix.
      responses:
        '200':
          description: File stored.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/UploadResponse'
        '400':
          $ref: '#/components/responses/StorageError'
        '500':
          $ref: '#/components/responses/StorageError'

  /api/v1/storage/{key}:
    parameters:
      - name: key
        in: path
        required: true
        allowReserved: true
        description: Slash-separated storage key returned by uploadFile.
        schema:
          type: string
          minLength: 1
        example: attachments/202607/8f3a2b1c9d4e5f6a.png
    get:
      tags: [Storage]
      operationId: downloadFile
      summary: Download a stored file
      security: []
      responses:
        '200':
          description: Raw file bytes with an extension-derived Content-Type.
          content:
            application/octet-stream:
              schema:
                type: string
                format: binary
        '404':
          $ref: '#/components/responses/StorageError'
        '500':
          $ref: '#/components/responses/StorageError'
    delete:
      tags: [Storage]
      operationId: deleteFile
      summary: Delete a stored file
      description: Delete is idempotent at the storage layer.
      security: []
      responses:
        '200':
          description: File deleted or already absent.
          content:
            application/json:
              schema:
                type: object
                required: [deleted]
                properties:
                  deleted:
                    type: string
              example:
                deleted: attachments/202607/8f3a2b1c9d4e5f6a.png
        '400':
          $ref: '#/components/responses/StorageError'
        '500':
          $ref: '#/components/responses/StorageError'

components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: Airway IM Airway-signed credential
    adminBearerAuth:
      type: http
      scheme: bearer
      bearerFormat: Airway IM administrator session token

  parameters:
    ConversationId:
      name: conversation_id
      in: path
      required: true
      schema:
        $ref: '#/components/schemas/ULID'

  schemas:
    ULID:
      type: string
      description: Opaque 26-character Crockford Base32 identifier.
      pattern: '^[0-9A-HJKMNP-TV-Z]{26}$'
      example: 01J2Q7D4N5R8TK6VD3SZ1H0Y9M

    User:
      type: object
      additionalProperties: false
      required: [id, uuid, username, nickname, avatar_url, email, last_seen_at, created_at, updated_at]
      properties:
        id:
          type: integer
          format: int64
        uuid:
          type: string
          description: Stable, unique identity assigned by the Airway application.
        username:
          type: string
        nickname:
          type: [string, 'null']
        avatar_url:
          type: [string, 'null']
          format: uri
        email:
          type: [string, 'null']
          format: email
        last_seen_at:
          type: [string, 'null']
          format: date-time
          description: Most recent authenticated activity, at five-minute granularity.
        created_at:
          type: string
          format: date-time
        updated_at:
          type: string
          format: date-time

    AdminUser:
      type: object
      additionalProperties: false
      required: [id, uuid, username, nickname, avatar_url, email, last_seen_at, token_version, created_at]
      properties:
        id:
          type: integer
          format: int64
        uuid:
          type: string
          description: Stable, unique identity assigned by the Airway application.
        username:
          type: string
        nickname:
          type: [string, 'null']
        avatar_url:
          type: [string, 'null']
          format: uri
        email:
          type: [string, 'null']
          format: email
        last_seen_at:
          type: [string, 'null']
          format: date-time
          description: Most recent authenticated activity, at five-minute granularity.
        token_version:
          type: integer
          format: int64
          description: Credential revocation counter; bumped by the revoke endpoint.
        created_at:
          type: string
          format: date-time

    Sender:
      type: object
      additionalProperties: false
      required: [id, username, nickname, avatar_url]
      properties:
        id:
          type: integer
          format: int64
        username:
          type: string
        nickname:
          type: [string, 'null']
        avatar_url:
          type: [string, 'null']
          format: uri

    Conversation:
      type: object
      additionalProperties: false
      required: [id, kind, title, avatar_url, created_by, created_at, updated_at]
      properties:
        id:
          $ref: '#/components/schemas/ULID'
        kind:
          type: string
          enum: [direct, group]
        title:
          type: [string, 'null']
        avatar_url:
          type: [string, 'null']
          format: uri
        created_by:
          type: integer
          format: int64
        created_at:
          type: string
          format: date-time
        updated_at:
          type: string
          format: date-time

    ConversationMember:
      type: object
      additionalProperties: false
      required: [id, uuid, username, nickname, avatar_url, role]
      properties:
        id:
          type: integer
          format: int64
        uuid:
          type: string
          description: Stable, unique identity assigned by the Airway application.
        username:
          type: string
        nickname:
          type: [string, 'null']
        avatar_url:
          type: [string, 'null']
          format: uri
        role:
          type: string
          enum: [owner, admin, member]

    ConversationDetails:
      type: object
      additionalProperties: false
      required: [conversation_uuid, type, members]
      properties:
        conversation_uuid:
          $ref: '#/components/schemas/ULID'
        type:
          type: string
          enum: [direct, group]
        members:
          type: array
          items:
            $ref: '#/components/schemas/ConversationMember'

    Message:
      type: object
      additionalProperties: false
      required:
        - id
        - conversation_id
        - sender
        - content
        - content_type
        - created_at
        - sequence
      properties:
        id:
          $ref: '#/components/schemas/ULID'
        conversation_id:
          $ref: '#/components/schemas/ULID'
        sender:
          $ref: '#/components/schemas/Sender'
        content:
          type: string
          minLength: 1
          description: >-
            UTF-8 source text limited to 32768 encoded bytes; line endings are
            normalized to LF. Illegal messages are returned as three asterisks.
            OpenAPI maxLength is intentionally omitted because it counts Unicode
            code points rather than encoded bytes.
        content_type:
          type: string
          enum: [text/markdown, text/plain]
        created_at:
          type: string
          format: date-time
        sequence:
          type: integer
          format: int64
          minimum: 1

    AdminConversation:
      type: object
      additionalProperties: false
      required:
        - id
        - kind
        - title
        - avatar_url
        - created_by
        - creator_username
        - member_count
        - message_count
        - latest_message_at
        - created_at
        - updated_at
      properties:
        id:
          $ref: '#/components/schemas/ULID'
        kind:
          type: string
          const: group
        title:
          type: [string, 'null']
        avatar_url:
          type: [string, 'null']
          format: uri
        created_by:
          type: integer
          format: int64
        creator_username:
          type: string
        member_count:
          type: integer
          format: int64
          minimum: 0
        message_count:
          type: integer
          format: int64
          minimum: 0
        latest_message_at:
          type: [string, 'null']
          format: date-time
        created_at:
          type: string
          format: date-time
        updated_at:
          type: string
          format: date-time

    AdminMessage:
      type: object
      additionalProperties: false
      required:
        - id
        - conversation_id
        - sender_id
        - sender_username
        - sender_nickname
        - sender_avatar_url
        - content
        - content_type
        - sequence
        - created_at
        - is_illegal
        - moderated_at
      properties:
        id:
          $ref: '#/components/schemas/ULID'
        conversation_id:
          $ref: '#/components/schemas/ULID'
        sender_id:
          type: integer
          format: int64
        sender_username:
          type: string
        sender_nickname:
          type: [string, 'null']
        sender_avatar_url:
          type: [string, 'null']
          format: uri
        content:
          type: string
        content_type:
          type: string
          enum: [text/markdown, text/plain]
        sequence:
          type: integer
          format: int64
          minimum: 1
        created_at:
          type: string
          format: date-time
        is_illegal:
          type: boolean
        moderated_at:
          type: [string, 'null']
          format: date-time

    AddMembersRequest:
      type: object
      additionalProperties: false
      required: [member_uuids]
      properties:
        member_uuids:
          type: array
          minItems: 1
          items:
            type: string
            minLength: 1
            maxLength: 64
          description: >-
            Users to add, by their stable uuid. Duplicates, empty values, and
            the authenticated user's own uuid are removed; at least one other
            user must remain. All uuids must exist; already-active members are
            skipped.

    CreateConversationRequest:
      type: object
      additionalProperties: false
      required: [kind, member_uuids]
      properties:
        kind:
          type: string
          enum: [direct, group]
        title:
          type: [string, 'null']
          description: Group display title; currently accepted for either kind.
        member_uuids:
          type: array
          minItems: 1
          items:
            type: string
            minLength: 1
            maxLength: 64
          description: >-
            Other users to add, by their stable uuid. Duplicates, empty values,
            and the authenticated user's own uuid are removed. Direct
            conversations must resolve to exactly one other user.

    CreateGroupRequest:
      type: object
      additionalProperties: false
      required: [member_uuids]
      properties:
        title:
          type: [string, 'null']
          description: Optional group display title.
        member_uuids:
          type: array
          minItems: 1
          items:
            type: string
            minLength: 1
            maxLength: 64
          description: >-
            Users to add as members, by their stable uuid. Duplicates, empty
            values, and the authenticated user's own uuid are removed; at least
            one other existing user must remain.

    CreateMessageRequest:
      type: object
      additionalProperties: false
      required: [conversation_id, content]
      properties:
        conversation_id:
          $ref: '#/components/schemas/ULID'
        content:
          type: string
          minLength: 1
          description: UTF-8 text limited to 32768 encoded bytes.
        content_type:
          type: string
          default: text/markdown
          enum: [text/markdown, text/plain]

    CreateConversationMessageRequest:
      type: object
      additionalProperties: false
      required: [content]
      properties:
        content:
          type: string
          minLength: 1
          description: UTF-8 text limited to 32768 encoded bytes.
        content_type:
          type: string
          default: text/markdown
          enum: [text/markdown, text/plain]

    ErrorEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          enum: [10000, 10001, 10003, 10005, 11001, 11002]
        data:
          type: 'null'
        message:
          type: string

    UserEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          const: 0
        data:
          $ref: '#/components/schemas/User'
        message:
          type: 'null'

    ConversationEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          const: 0
        data:
          $ref: '#/components/schemas/Conversation'
        message:
          type: 'null'

    ConversationDetailsEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          const: 0
        data:
          $ref: '#/components/schemas/ConversationDetails'
        message:
          type: 'null'

    ConversationListEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          const: 0
        data:
          type: array
          items:
            $ref: '#/components/schemas/Conversation'
        message:
          type: 'null'

    MessageEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          const: 0
        data:
          $ref: '#/components/schemas/Message'
        message:
          type: 'null'

    MessageListEnvelope:
      type: object
      additionalProperties: false
      required: [code, data, message]
      properties:
        code:
          type: integer
          const: 0
        data:
          type: array
          items:
            $ref: '#/components/schemas/Message'
        message:
          type: 'null'

    UploadResponse:
      type: object
      additionalProperties: false
      required: [key, url, size]
      properties:
        key:
          type: string
        url:
          type: string
          description: Local path, public URL, presigned URL, or empty on URL generation failure.
        size:
          type: integer
          format: int64
          minimum: 0

    StorageError:
      type: object
      additionalProperties: false
      required: [error]
      properties:
        error:
          type: string

  responses:
    InvalidBearerToken:
      description: Missing, malformed, invalid, or expired Airway IM credential.
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/ErrorEnvelope'
          example:
            code: 10001
            data: null
            message: Invalid bearer token

    InternalError:
      description: Unexpected backend error.
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/ErrorEnvelope'
          example:
            code: 10000
            data: null
            message: Could not complete request

    StorageError:
      description: Storage request error.
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/StorageError'
```

## WebSocket Gateway companion contract

OpenAPI does not describe the long-lived Gateway frames. The macOS client
connects to `ws://127.0.0.1:1910/ws` locally and must send authentication as its
first text frame within 10 seconds:

```json
{"cmd":"auth","opts":["<credential>"]}
```

Success:

```json
{"code":0,"data":"OK"}
```

The client creates messages through `POST /api/v1/messages`, not through the
socket. A committed message is delivered at least once as a server event:

```json
{
  "event_id": "01J2Q8A4FQ8NA8R6YDJ2M98K3P",
  "event": "message.created",
  "message_id": "01J2Q8A4FQ8NA8R6YDJ2M98K3Q",
  "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
  "sequence": 1042,
  "targets": {"user_ids":[1,2]}
}
```

When an administrator marks an existing message as illegal, the Gateway sends
the same routing fields with `"event": "message.moderated"`. Because that
sequence may already be cached, clients reload starting at `sequence - 1` and
replace the local message with the masked API response.

When an owner or admin adds members to a group, the Gateway fans out a
`conversation.member_added` event to every active member, including the ones
just added:

```json
{
  "event_id": "01J2Q8A4FQ8NA8R6YDJ2M98K3R",
  "event": "conversation.member_added",
  "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
  "added_user_ids": [4, 5],
  "targets": {"user_ids":[1,2,4,5]}
}
```

Clients should refresh the member list via
`GET /api/v1/conversations/:uuid`; a newly added client sees the group in its
conversation list and can read history through the message endpoint.

A removal fans out `conversation.member_removed` to every active member plus
the removed user (so their client learns it was kicked):

```json
{
  "event_id": "01J2Q8A4FQ8NA8R6YDJ2M98K3S",
  "event": "conversation.member_removed",
  "conversation_id": "01J2Q7D4N5R8TK6VD3SZ1H0Y9M",
  "removed_user_id": 5,
  "targets": {"user_ids":[1,2,5]}
}
```

A removed client immediately loses access: listing the group, reading its
messages, and sending all fail with 403/404 from then on.

Client requirements:

- deduplicate HTTP and WebSocket results by `message_id`;
- deduplicate repeated delivery events by `event_id`;
- order messages by `(conversation_id, sequence)`;
- when a sequence gap is observed, call the message history endpoint with the
  last stored sequence; and
- reconnect and authenticate again after a socket close.

The full connection lifecycle and operational design are documented in
[`../design/gateway.md`](../design/gateway.md).
