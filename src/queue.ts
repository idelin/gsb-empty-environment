import type { Clock } from "./clock.ts";

export type Priority = "polite" | "urgent";

/** A unit of status to be announced by assistive technology. */
export interface StatusMessage {
  /** Unique message id. */
  id: string;
  /** Status entity this message reports on (e.g. "upload-42"). */
  entityId: string;
  /** Entity version; lower versions of the same entity are stale. */
  version: number;
  /** Delivery acknowledgement id; retries reuse it and must not double-ack. */
  ackId: string;
  /** Owning component; unmounting it discards its pending messages. */
  ownerId: string;
  priority: Priority;
  text: string;
  /** Messages with the same merge key and same progress coalesce, latest wins. */
  mergeKey?: string;
  progress?: number;
  /** Time-to-live in ms, relative to enqueue time on the injected clock. */
  ttlMs?: number;
}

export type EnqueueResult =
  | "queued"
  | "merged"
  | "dropped-stale"
  | "dropped-retracted"
  | "duplicate-ack";

export type DropReason =
  | "expired"
  | "retracted"
  | "superseded"
  | "canceled"
  | "unmounted"
  | "duplicate";

export interface AnnounceEvent {
  text: string;
  message: StatusMessage;
  /** True when this announcement resumes a polite message paused by an urgent one. */
  resumed: boolean;
  at: number;
}

export interface QueueOptions {
  clock: Clock;
  announce: (event: AnnounceEvent) => void;
  /** Completion callback ("回包"); fires exactly once per ackId. */
  onAck?: (ackId: string, message: StatusMessage) => void;
  onDrop?: (message: StatusMessage, reason: DropReason) => void;
  /** After this many consecutive urgent announcements, a waiting polite
   *  message is served first. Guarantees urgent traffic cannot permanently
   *  starve polite messages. Defaults to 2. */
  urgentQuota?: number;
}

interface EntityState {
  latestVersion: number;
  retracted: boolean;
}

interface Entry {
  msg: StatusMessage;
  expiresAt?: number;
  paused: boolean;
  dead: boolean;
}

export class StatusAnnounceQueue {
  #clock: Clock;
  #announce: (event: AnnounceEvent) => void;
  #onAck?: (ackId: string, message: StatusMessage) => void;
  #onDrop?: (message: StatusMessage, reason: DropReason) => void;
  #urgentQuota: number;

  #polite: Entry[] = [];
  #urgent: Entry[] = [];
  #byId = new Map<string, Entry>();
  #byAckId = new Map<string, Entry>();
  #entities = new Map<string, EntityState>();
  #ackedAckIds = new Set<string>();
  #current: Entry | null = null;
  #urgentStreak = 0;

  constructor(options: QueueOptions) {
    this.#clock = options.clock;
    this.#announce = options.announce;
    this.#onAck = options.onAck;
    this.#onDrop = options.onDrop;
    this.#urgentQuota = Math.max(1, options.urgentQuota ?? 2);
  }

  /** Message currently being announced, if any. */
  get current(): StatusMessage | null {
    return this.#current?.msg ?? null;
  }

  get pendingCount(): number {
    return this.#byId.size;
  }

  get idle(): boolean {
    return this.#current === null && this.#byId.size === 0;
  }

  enqueue(msg: StatusMessage): EnqueueResult {
    const entity = this.#entity(msg.entityId);
    if (entity.retracted) return "dropped-retracted";
    // Retry of an already-acked or in-flight delivery: never re-ack, never re-announce.
    if (this.#ackedAckIds.has(msg.ackId) || this.#byAckId.has(msg.ackId)) {
      return "duplicate-ack";
    }
    if (msg.version < entity.latestVersion) return "dropped-stale";
    entity.latestVersion = msg.version;

    // Version correction: older queued/current versions of this entity are stale now.
    for (const entry of this.#byId.values()) {
      if (entry.msg.entityId === msg.entityId && entry.msg.version < msg.version) {
        this.#kill(entry, "superseded");
      }
    }
    if (this.#current?.dead) this.#current = null;

    // Same merge key + same progress: coalesce, keep the latest message.
    let merged = false;
    if (msg.mergeKey !== undefined) {
      for (const entry of this.#byId.values()) {
        if (
          entry.msg.mergeKey === msg.mergeKey &&
          entry.msg.progress === msg.progress &&
          entry.msg.ownerId === msg.ownerId
        ) {
          this.#kill(entry, "superseded");
          merged = true;
        }
      }
    }
    if (this.#current?.dead) this.#current = null;

    const entry: Entry = {
      msg,
      expiresAt: msg.ttlMs === undefined ? undefined : this.#clock.now() + msg.ttlMs,
      paused: false,
      dead: false,
    };
    this.#byId.set(msg.id, entry);
    this.#byAckId.set(msg.ackId, entry);

    // Urgent interrupts an in-flight polite announcement; the polite message
    // is paused and re-validated before it resumes.
    if (msg.priority === "urgent" && this.#current?.msg.priority === "polite") {
      const paused = this.#current;
      paused.paused = true;
      this.#current = null;
      this.#polite.unshift(paused);
    }

    (msg.priority === "urgent" ? this.#urgent : this.#polite).push(entry);
    this.#pump();
    return merged ? "merged" : "queued";
  }

  /** Cancel a pending (or in-flight) message by id or ackId. It will never be announced. */
  cancel(idOrAckId: string): boolean {
    const entry = this.#byId.get(idOrAckId) ?? this.#byAckId.get(idOrAckId);
    if (entry) {
      if (entry === this.#current) {
        this.#killCurrent("canceled");
        this.#pump();
      } else {
        this.#kill(entry, "canceled");
      }
      return true;
    }
    return false;
  }

  /** Entity retraction invalidates every queued message of that entity. */
  retractEntity(entityId: string): number {
    const entity = this.#entity(entityId);
    entity.retracted = true;
    let dropped = 0;
    for (const entry of this.#byId.values()) {
      if (entry.msg.entityId === entityId) {
        this.#kill(entry, "retracted");
        dropped += 1;
      }
    }
    if (this.#current?.dead) this.#current = null;
    this.#pump();
    return dropped;
  }

  /** Component unmount discards all of its pending announcements. */
  unmount(ownerId: string): number {
    let dropped = 0;
    for (const entry of this.#byId.values()) {
      if (entry.msg.ownerId === ownerId) {
        this.#kill(entry, "unmounted");
        dropped += 1;
      }
    }
    if (this.#current?.dead) this.#current = null;
    this.#pump();
    return dropped;
  }

  /** Complete the in-flight announcement: fires the completion callback
   *  exactly once per ackId, then schedules the next message. */
  finishCurrent(): boolean {
    if (!this.#current) return false;
    const entry = this.#current;
    this.#current = null;
    this.#byId.delete(entry.msg.id);
    this.#byAckId.delete(entry.msg.ackId);
    if (!this.#ackedAckIds.has(entry.msg.ackId)) {
      this.#ackedAckIds.add(entry.msg.ackId);
      this.#onAck?.(entry.msg.ackId, entry.msg);
    }
    this.#pump();
    return true;
  }

  /** Drain the queue under fair scheduling; returns when idle. */
  runToCompletion(maxSteps = 10_000): void {
    let steps = 0;
    while (!this.idle) {
      if (steps++ > maxSteps) throw new Error("runToCompletion: step budget exceeded");
      if (this.#current) this.finishCurrent();
      else this.#pump();
    }
  }

  // ---- internals -------------------------------------------------------

  #entity(entityId: string): EntityState {
    let entity = this.#entities.get(entityId);
    if (!entity) {
      entity = { latestVersion: -Infinity, retracted: false };
      this.#entities.set(entityId, entity);
    }
    return entity;
  }

  /** Reason the entry is no longer announceable, or null if it is still live. */
  #staleness(entry: Entry): DropReason | null {
    const entity = this.#entities.get(entry.msg.entityId);
    if (entity?.retracted) return "retracted";
    if (entity && entry.msg.version < entity.latestVersion) return "superseded";
    if (entry.expiresAt !== undefined && this.#clock.now() > entry.expiresAt) return "expired";
    return null;
  }

  #kill(entry: Entry, reason: DropReason): void {
    if (entry.dead) return;
    entry.dead = true;
    this.#byId.delete(entry.msg.id);
    this.#byAckId.delete(entry.msg.ackId);
    this.#onDrop?.(entry.msg, reason);
  }

  #killCurrent(reason: DropReason): void {
    if (!this.#current) return;
    const entry = this.#current;
    this.#current = null;
    this.#kill(entry, reason);
  }

  /** Pop the front entry of `queue`, skipping lazily-deleted entries. */
  #takeLive(queue: Entry[]): Entry | null {
    while (queue.length > 0) {
      const entry = queue.shift()!;
      if (entry.dead) continue;
      const stale = this.#staleness(entry);
      if (stale !== null) {
        this.#kill(entry, stale);
        continue;
      }
      return entry;
    }
    return null;
  }

  #peekLive(queue: Entry[]): Entry | null {
    for (const entry of queue) {
      if (!entry.dead) return entry;
    }
    return null;
  }

  /** Fair pick: urgent first, but after `urgentQuota` consecutive urgent
   *  announcements a waiting polite message is served. */
  #pickNext(): Entry | null {
    const politeWaiting = this.#peekLive(this.#polite) !== null;
    if (this.#urgentStreak >= this.#urgentQuota && politeWaiting) {
      this.#urgentStreak = 0;
      return this.#takeLive(this.#polite);
    }
    const urgent = this.#takeLive(this.#urgent);
    if (urgent) {
      this.#urgentStreak += 1;
      return urgent;
    }
    this.#urgentStreak = 0;
    return this.#takeLive(this.#polite);
  }

  #pump(): void {
    if (this.#current) return;
    const entry = this.#pickNext();
    if (!entry) return;
    this.#current = entry;
    // Paused polite messages are re-validated above (via #takeLive → #staleness)
    // before this resume announcement is emitted.
    this.#announce({
      text: entry.msg.text,
      message: entry.msg,
      resumed: entry.paused,
      at: this.#clock.now(),
    });
  }
}
