import type { Clock } from './clock.ts';

export type DemotionReason = 'lease-expired' | 'clock-jump';

export interface LeaseConfig {
  /** Lease time-to-live in ms (e.g. 10_000 for a 10s lease). */
  ttlMs: number;
  /** Maximum tolerated clock drift between nodes in ms (e.g. 500). */
  maxClockDriftMs: number;
  /** Worst-case communication round-trip time to the quorum in ms. */
  maxRttMs?: number;
}

export interface LeaseEvent {
  type: 'acquired' | 'write' | 'write-rejected' | 'demoted';
  term: number;
  atMonoMs: number;
  reason?: DemotionReason;
  command?: string;
}

export type LeaseListener = (event: LeaseEvent) => void;

type Role = 'follower' | 'primary';

/**
 * LeaseManager: the primary-side kernel of a distributed lease protocol.
 *
 * Safety model:
 *  - The quorum grants a lease of `ttlMs` starting at acquire time.
 *  - Because other nodes' clocks may run up to `maxClockDriftMs` fast and a
 *    grant/renew message may take up to `maxRttMs` on the wire, the primary
 *    only considers itself authoritative during the SAFE WINDOW:
 *
 *        safeWindow = ttlMs - maxClockDriftMs - maxRttMs
 *
 *    With ttl=10s and drift=500ms the primary stops issuing writes at 9.5s.
 *  - All expiry arithmetic uses the monotonic clock, so wall-clock jumps
 *    cannot extend the lease.
 *  - A self-check compares wall-clock progress against monotonic progress.
 *    If the wall clock jumps by more than `maxClockDriftMs` (in either
 *    direction), the primary can no longer reason about the quorum's view
 *    of time and performs an EMERGENCY DEMOTION, fencing itself off before
 *    any split-brain (two primaries) can occur.
 */
export class LeaseManager {
  private readonly clock: Clock;
  private readonly ttlMs: number;
  private readonly maxDriftMs: number;
  private readonly maxRttMs: number;
  private readonly listeners: LeaseListener[] = [];

  private role: Role = 'follower';
  private term = 0;
  private safeUntilMono = 0;
  private lastWallMs = 0;
  private lastMonoMs = 0;

  constructor(clock: Clock, config: LeaseConfig) {
    if (config.ttlMs <= config.maxClockDriftMs + (config.maxRttMs ?? 0)) {
      throw new Error('ttlMs must exceed maxClockDriftMs + maxRttMs');
    }
    this.clock = clock;
    this.ttlMs = config.ttlMs;
    this.maxDriftMs = config.maxClockDriftMs;
    this.maxRttMs = config.maxRttMs ?? 0;
  }

  onEvent(listener: LeaseListener): void {
    this.listeners.push(listener);
  }

  private emit(event: LeaseEvent): void {
    for (const listener of this.listeners) listener(event);
  }

  /** Acquire (or renew) a lease granted by the quorum for a new term. */
  acquire(term: number): void {
    this.term = term;
    this.role = 'primary';
    const nowMono = this.clock.monotonicNow();
    this.safeUntilMono =
      nowMono + (this.ttlMs - this.maxDriftMs - this.maxRttMs);
    this.lastMonoMs = nowMono;
    this.lastWallMs = this.clock.wallNow();
    this.emit({ type: 'acquired', term, atMonoMs: nowMono });
  }

  /** Voluntarily give up primary privileges. */
  relinquish(): void {
    if (this.role === 'primary') {
      this.role = 'follower';
      this.emit({
        type: 'demoted',
        term: this.term,
        atMonoMs: this.clock.monotonicNow(),
        reason: 'lease-expired',
      });
    }
  }

  /**
   * Safety self-check. Detects wall-clock jumps by comparing wall progress
   * against monotonic progress since the previous check. Any divergence
   * beyond maxClockDriftMs forces an emergency demotion.
   */
  selfCheck(): void {
    if (this.role !== 'primary') return;

    const wall = this.clock.wallNow();
    const mono = this.clock.monotonicNow();
    const jumpMs = (wall - this.lastWallMs) - (mono - this.lastMonoMs);
    this.lastWallMs = wall;
    this.lastMonoMs = mono;

    if (Math.abs(jumpMs) > this.maxDriftMs) {
      this.role = 'follower';
      this.emit({
        type: 'demoted',
        term: this.term,
        atMonoMs: mono,
        reason: 'clock-jump',
      });
      return;
    }

    if (mono >= this.safeUntilMono) {
      this.role = 'follower';
      this.emit({
        type: 'demoted',
        term: this.term,
        atMonoMs: mono,
        reason: 'lease-expired',
      });
    }
  }

  /** True only while inside the safe window with no clock anomaly. */
  isPrimary(): boolean {
    this.selfCheck();
    return this.role === 'primary';
  }

  /**
   * Issue a write as primary. Throws if the safe window has passed or a
   * clock anomaly has been detected — the write is fenced off.
   */
  write(command: string): void {
    const mono = this.clock.monotonicNow();
    if (!this.isPrimary()) {
      this.emit({ type: 'write-rejected', term: this.term, atMonoMs: mono, command });
      throw new Error(
        `fenced: not primary (term ${this.term}), refusing write "${command}"`,
      );
    }
    this.emit({ type: 'write', term: this.term, atMonoMs: mono, command });
  }

  /** Remaining safe-window time in ms (0 when not primary). */
  safeTimeRemainingMs(): number {
    if (this.role !== 'primary') return 0;
    return Math.max(0, this.safeUntilMono - this.clock.monotonicNow());
  }
}
