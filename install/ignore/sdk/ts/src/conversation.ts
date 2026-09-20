// Conversation objects: one object per conversation with its own
// event emitter, so a chat window subscribes directly instead of filtering
// the global stream by conversation id. The kind lives on the object
// (DirectConversation / GroupConversation), and the first "message"
// listener starts tracking the conversation automatically. Conversation
// objects are cached per conversation id inside AirwayIM: opening the same
// conversation twice yields the same object with its listeners.

import type { AirwayIM } from "./session.js";
import type { MembersAddedInfo, MembersRemovedInfo } from "./session.js";
import type { SendMessageOptions } from "./http.js";
import type { ChatMessage, ConversationDetails, ConversationKind } from "./types.js";

export interface ConversationEvents {
  /** New messages in this conversation: ordered, deduplicated, gap-filled. */
  message: (message: ChatMessage, source: "history" | "realtime") => void;
  /** A previously seen message in this conversation was masked (content "***"). */
  "message.updated": (message: ChatMessage) => void;
  /** Group conversations only: members were added (includes the added users). */
  "members.added": (info: MembersAddedInfo) => void;
  /** Group conversations only: a member was removed (the kicked user is notified too). */
  "members.removed": (info: MembersRemovedInfo) => void;
}

type EventName = keyof ConversationEvents;

type Listener<K extends EventName> = ConversationEvents[K] extends (...args: infer A) => void
  ? (...args: A) => void
  : never;

export class Conversation {
  readonly id: string;
  readonly kind: ConversationKind;
  private readonly listeners = new Map<EventName, Set<Listener<EventName>>>();

  constructor(
    /** The owning facade; internal, not part of the public surface. */
    protected readonly im: AirwayIM,
    id: string,
    kind: ConversationKind,
  ) {
    this.id = id;
    this.kind = kind;
  }

  /**
   * Subscribe to this conversation's events. The first "message" listener
   * starts tracking the conversation automatically: it fetches everything
   * since the last persisted cursor (history() semantics) and keeps the
   * conversation in sync from then on.
   */
  on<K extends EventName>(event: K, listener: Listener<K>): this {
    let set = this.listeners.get(event);
    if (!set) {
      set = new Set();
      this.listeners.set(event, set);
    }
    const wasEmpty = set.size === 0;
    set.add(listener as Listener<EventName>);
    if (wasEmpty && event === "message") this.im.ensureTracked(this.id);
    return this;
  }

  off<K extends EventName>(event: K, listener: Listener<K>): this {
    this.listeners.get(event)?.delete(listener as Listener<EventName>);
    return this;
  }

  /**
   * Initial load: fetch messages since the last persisted cursor (or
   * fromSequence) and emit them here and on the global stream with source
   * "history". Mostly redundant after on("message") — that already
   * auto-tracks — but useful to await the backlog before rendering.
   */
  history(options: { fromSequence?: number; limit?: number } = {}): Promise<ChatMessage[]> {
    return this.im.history(this.id, options);
  }

  /** Send a message to this conversation (idempotency semantics as sendGroupMessage). */
  send(content: string, options: SendMessageOptions = {}): Promise<ChatMessage> {
    return this.im.rest.sendMessage(this.id, content, options);
  }

  /** Conversation kind plus members with roles, fresh from the API. */
  details(): Promise<ConversationDetails> {
    return this.im.rest.getConversation(this.id);
  }

  /** Last synced sequence in this conversation. */
  lastSequence(): number {
    return this.im.lastSequence(this.id);
  }

  /** Drop all sync state (e.g. after being kicked from the group). */
  forget(): void {
    this.im.forgetConversation(this.id);
  }

  /** @internal AirwayIM routes global engine events here. */
  emitLocal<K extends EventName>(event: K, ...args: Parameters<Listener<K>>): void {
    const set = this.listeners.get(event);
    if (!set) return;
    for (const listener of [...set]) {
      try {
        (listener as (...a: unknown[]) => void)(...args);
      } catch (err) {
        this.im.reportError(err as Error);
      }
    }
  }
}

/** A direct (1:1) conversation; the peer of an incoming message is msg.sender. */
export class DirectConversation extends Conversation {
  declare readonly kind: "direct";

  constructor(im: AirwayIM, id: string) {
    super(im, id, "direct");
  }
}

/** A group conversation; carries group-only operations. */
export class GroupConversation extends Conversation {
  declare readonly kind: "group";
  /** Title from creation time (null when created without one or when opened
   * by id); details() returns the live value. */
  readonly title: string | null;

  constructor(im: AirwayIM, id: string, title: string | null = null) {
    super(im, id, "group");
    this.title = title;
  }

  /** Add members by uuid (owner/admin; idempotent for active members). */
  addMembers(memberUUIDs: string[]): Promise<ConversationDetails> {
    return this.im.addMembers(this.id, memberUUIDs);
  }

  /** Remove members by uuid (owner/admin; cannot remove self or the owner). */
  removeMembers(userUUIDs: string[]): Promise<ConversationDetails> {
    return this.im.removeMembers(this.id, userUUIDs);
  }
}
