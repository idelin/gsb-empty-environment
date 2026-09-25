import type { Clock } from './clock.ts';
import type { DropReason, Priority, StatusEntity, StatusMessage } from './types.ts';

export interface AnnouncerHooks {
  /**
   * Deliver (speak/render) the message. Return false (or throw) to signal a
   * transient failure; the announcer retries after `retryDelayMs`. The ack
   * for a message is emitted exactly once, no matter how many retries occur.
   */
  speak(message: StatusMessage): boolean | void;
  onStart?(message: StatusMessage): void;
  onComplete?(message: StatusMessage): void;
  onAck?(ackId: string): void;
  onDrop?(message: StatusMessage, reason: DropReason): void;
  onPause?(message: StatusMessage): void;
  onResume?(message: StatusMessage): void;
}

export interface AnnouncerOptions {
  clock: Clock;
  hooks: AnnouncerHooks;
  /** Max consecutive urgent announcements before a polite one is served. */
  urgentStreakLimit?: number;
  retryDelayMs?: number;
  /** How long speaking a message takes; drives interruption windows. */
  estimateDurationMs?: (message: StatusMessage) => number;
}

interface SpeakingState {
  message: StatusMessage;
  timer: number;
  /** Absolute time (on the injected clock) at which the timer fires. */
  deadline: number;
  /** 'speak' counts down to completion; 'retry' counts down to a re-attempt. */
  phase: 'speak' | 'retry';
}

interface PausedMessage {
  message: StatusMessage;
  remainingMs: number;
  phase: 'speak' | 'retry';
}

const DEFAULT_URGENT_STREAK_LIMIT = 2;
const DEFAULT_RETRY_DELAY_MS = 100;

/**
 * Status message queue for assistive technology.
 *
 * - Two priorities: urgent interrupts polite; paused polite messages are
 *   re-validated (expiry / retraction / version) before they resume.
 * - Merge keys coalesce same-progress messages, keeping the newest.
 * - Delivery retries never produce a duplicate acknowledgement.
 * - Fair scheduling: an urgent streak is bounded so polite messages
 *   cannot starve.
 */
export class StatusAnnouncer {
  private readonly clock: Clock;
  private readonly hooks: AnnouncerHooks;
  private readonly urgentStreakLimit: number;
  private readonly retryDelayMs: number;
  private readonly estimateDurationMs: (message: StatusMessage) => number;

  private readonly entities = new Map<string, StatusEntity>();
  private readonly pending = new Map<string, StatusMessage>();
  private readonly queues: Record<Priority, string[]> = { polite: [], urgent: [] };
  private readonly mergeIndex = new Map<string, string>();
  private readonly paused: PausedMessage[] = [];
  private readonly acked = new Set<string>();

  private speaking: SpeakingState | null = null;
  private urgentStreak = 0;

  constructor(options: AnnouncerOptions) {
    this.clock = options.clock;
    this.hooks = options.hooks;
    this.urgentStreakLimit = options.urgentStreakLimit ?? DEFAULT_URGENT_STREAK_LIMIT;
    this.retryDelayMs = options.retryDelayMs ?? DEFAULT_RETRY_DELAY_MS;
    this.estimateDurationMs =
      options.estimateDurationMs ?? ((m) => Math.max(50, m.text.length * 30));
  }

  // ---------------------------------------------------------------- entities

  registerEntity(entityId: string, version = 0): StatusEntity {
    const existing = this.entities.get(entityId);
    if (existing) return existing;
    const entity: StatusEntity = { id: entityId, version, retracted: false };
    this.entities.set(entityId, entity);
    return entity;
  }

  /** Version correction: queued messages older than `version` become stale. */
  correctEntityVersion(entityId: string, version: number): void {
    const entity = this.registerEntity(entityId);
    if (version <= entity.version) return;
    entity.version = version;
    this.purgeWhere(
      (m) => m.entityId === entityId && m.version < version,
      'stale-version',
    );
  }

  /** Retraction invalidates every queued/paused/in-flight message. */
  retractEntity(entityId: string): void {
    const entity = this.registerEntity(entityId);
    entity.retracted = true;
    this.purgeWhere((m) => m.entityId === entityId, 'entity-retracted');
  }

  // ------------------------------------------------------------------ intake

  /** Returns true when the message was accepted into the queue. */
  enqueue(message: StatusMessage): boolean {
    const entity = this.registerEntity(message.entityId);
    const stamped: StatusMessage = { ...message, enqueuedAt: this.clock.now() };

    const reject = this.validate(stamped, entity);
    if (reject) {
      this.hooks.onDrop?.(stamped, reject);
      return false;
    }

    if (stamped.mergeKey !== undefined) {
      const previousId = this.mergeIndex.get(stamped.mergeKey);
      if (previousId !== undefined) {
        const previous = this.pending.get(previousId);
        if (previous) this.dropMessage(previous, 'merged');
        this.mergeIndex.delete(stamped.mergeKey);
      }
      this.mergeIndex.set(stamped.mergeKey, stamped.id);
    }

    this.pending.set(stamped.id, stamped);
    this.queues[stamped.priority].push(stamped.id);

    // Urgent interrupts polite speech — but only while the urgent streak is
    // below the fairness limit, otherwise a relentless urgent producer could
    // keep interrupting the same polite message forever.
    if (
      stamped.priority === 'urgent' &&
      this.speaking?.message.priority === 'polite' &&
      this.urgentStreak < this.urgentStreakLimit
    ) {
      this.interruptSpeaking();
    }
    if (!this.speaking) this.schedule();
    return true;
  }

  /** Cancel a status: queued/paused/in-flight messages are never announced. */
  cancelEntity(entityId: string): void {
    this.purgeWhere((m) => m.entityId === entityId, 'cancelled');
  }

  cancelMessage(messageId: string): void {
    this.purgeWhere((m) => m.id === messageId, 'cancelled');
  }

  /** Component unmount cleans up everything it still owns. */
  unmount(ownerId: string): void {
    this.purgeWhere((m) => m.ownerId === ownerId, 'owner-unmounted');
  }

  // ------------------------------------------------------------------ status

  isAcknowledged(ackId: string): boolean {
    return this.acked.has(ackId);
  }

  /** A message is live iff announcing it right now would not be stale. */
  isLive(message: StatusMessage): boolean {
    const entity = this.entities.get(message.entityId);
    return entity !== undefined && this.validate(message, entity) === null;
  }

  inspect(): { queued: number; paused: number; speaking: string | null } {
    return {
      queued: this.pending.size,
      paused: this.paused.length,
      speaking: this.speaking?.message.id ?? null,
    };
  }

  // ----------------------------------------------------------------- engine

  private validate(message: StatusMessage, entity: StatusEntity): DropReason | null {
    if (entity.retracted) return 'entity-retracted';
    if (message.version < entity.version) return 'stale-version';
    const enqueuedAt = message.enqueuedAt ?? this.clock.now();
    if (enqueuedAt + message.ttlMs <= this.clock.now()) return 'expired';
    return null;
  }

  private dropMessage(message: StatusMessage, reason: DropReason): void {
    this.removeFromQueue(message.id);
    if (this.pending.delete(message.id)) {
      if (message.mergeKey !== undefined) {
        this.mergeIndex.delete(message.mergeKey);
      }
      this.hooks.onDrop?.(message, reason);
    }
  }

  private removeFromQueue(messageId: string): void {
    for (const priority of ['polite', 'urgent'] as const) {
      const queue = this.queues[priority];
      const index = queue.indexOf(messageId);
      if (index !== -1) queue.splice(index, 1);
    }
  }

  private purgeWhere(
    predicate: (message: StatusMessage) => boolean,
    reason: DropReason,
  ): void {
    for (const message of [...this.pending.values()]) {
      if (predicate(message)) this.dropMessage(message, reason);
    }
    for (let i = this.paused.length - 1; i >= 0; i--) {
      if (predicate(this.paused[i].message)) {
        const [entry] = this.paused.splice(i, 1);
        this.hooks.onDrop?.(entry.message, reason);
      }
    }
    if (this.speaking && predicate(this.speaking.message)) {
      const stopped = this.speaking;
      this.clock.clearTimeout(stopped.timer);
      this.speaking = null;
      this.hooks.onDrop?.(stopped.message, reason);
      this.schedule();
    }
  }

  private interruptSpeaking(): void {
    const current = this.speaking;
    if (!current) return;
    this.clock.clearTimeout(current.timer);
    this.speaking = null;
    this.paused.push({
      message: current.message,
      remainingMs: this.remainingOf(current),
      phase: current.phase,
    });
    this.hooks.onPause?.(current.message);
  }

  private remainingOf(state: SpeakingState): number {
    return Math.max(0, state.deadline - this.clock.now());
  }

  /** Pop dead entries off the front of a queue; return the live head. */
  private peekLive(priority: Priority): StatusMessage | null {
    const queue = this.queues[priority];
    while (queue.length > 0) {
      const message = this.pending.get(queue[0]);
      if (!message) {
        queue.shift();
        continue;
      }
      const entity = this.entities.get(message.entityId)!;
      const invalid = this.validate(message, entity);
      if (invalid) {
        queue.shift();
        this.dropMessage(message, invalid);
        continue;
      }
      return message;
    }
    return null;
  }

  private takeLive(priority: Priority): StatusMessage | null {
    const message = this.peekLive(priority);
    if (message) {
      this.queues[priority].shift();
      this.pending.delete(message.id);
      if (message.mergeKey !== undefined) this.mergeIndex.delete(message.mergeKey);
    }
    return message;
  }

  /** Re-validate the paused stack top; drop stale entries, return live one. */
  private peekPausedLive(): PausedMessage | null {
    while (this.paused.length > 0) {
      const entry = this.paused[this.paused.length - 1];
      const entity = this.entities.get(entry.message.entityId)!;
      const invalid = this.validate(entry.message, entity);
      if (invalid) {
        this.paused.pop();
        this.hooks.onDrop?.(entry.message, invalid);
        continue;
      }
      return entry;
    }
    return null;
  }

  private schedule(): void {
    if (this.speaking) return;

    const urgent = this.peekLive('urgent');
    const pausedPolite = this.peekPausedLive();
    const polite = pausedPolite === null ? this.peekLive('polite') : null;
    const politeAvailable = pausedPolite !== null || polite !== null;

    // Fair scheduling: urgent wins unless the streak limit is reached and a
    // polite message is waiting — so urgent traffic can never starve polite.
    if (urgent && (this.urgentStreak < this.urgentStreakLimit || !politeAvailable)) {
      this.startSpeaking(this.takeLive('urgent')!);
      return;
    }
    if (pausedPolite) {
      this.paused.pop();
      this.resumeSpeaking(pausedPolite);
      return;
    }
    if (polite) {
      this.startSpeaking(this.takeLive('polite')!);
      return;
    }
    if (urgent) {
      this.startSpeaking(this.takeLive('urgent')!);
    }
  }

  private startSpeaking(message: StatusMessage): void {
    this.hooks.onStart?.(message);
    let ok = true;
    try {
      ok = this.hooks.speak(message) !== false;
    } catch {
      ok = false;
    }
    if (ok) {
      this.armTimer(message, 'speak', this.estimateDurationMs(message));
    } else {
      this.armTimer(message, 'retry', this.retryDelayMs);
    }
  }

  private resumeSpeaking(entry: PausedMessage): void {
    this.hooks.onResume?.(entry.message);
    this.armTimer(entry.message, entry.phase, entry.remainingMs);
  }

  private armTimer(
    message: StatusMessage,
    phase: 'speak' | 'retry',
    delayMs: number,
  ): void {
    const state: SpeakingState = {
      message,
      timer: -1,
      deadline: this.clock.now() + delayMs,
      phase,
    };
    state.timer = this.clock.setTimeout(() => this.onTimer(state), delayMs);
    this.speaking = state;
  }

  private onTimer(state: SpeakingState): void {
    if (this.speaking !== state) return; // superseded timer
    this.speaking = null;
    const message = state.message;
    const entity = this.entities.get(message.entityId)!;
    const invalid = this.validate(message, entity);
    if (invalid) {
      this.hooks.onDrop?.(message, invalid);
      this.schedule();
      return;
    }
    if (state.phase === 'retry') {
      this.startSpeaking(message);
      return;
    }
    this.complete(message);
  }

  private complete(message: StatusMessage): void {
    // Update the fairness counter before invoking hooks: hooks may enqueue
    // follow-up messages, which can synchronously trigger schedule().
    if (message.priority === 'urgent') {
      this.urgentStreak += 1;
    } else {
      this.urgentStreak = 0;
    }
    if (!this.acked.has(message.ackId)) {
      this.acked.add(message.ackId);
      this.hooks.onAck?.(message.ackId);
    }
    this.hooks.onComplete?.(message);
    this.schedule();
  }
}
