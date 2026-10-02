import { EventEmitter } from 'node:events';
import { systemClocks, type ClockPair } from './clocks.ts';
import { LeaseManager } from './lease-manager.ts';

export type GuardianMode = 'normal' | 'safe';

export type TripReason =
  | 'WALL_CLOCK_BACKWARD' // manual set-back / VM snapshot restore
  | 'CLOCK_TEAR' // wall and monotonic advanced at different paces (NTP step)
  | 'WALL_CLOCK_STALLED' // wall clock frozen while monotonic advanced
  | 'MONOTONIC_STALLED'; // monotonic frozen while wall advanced (VM suspend)

export interface TripEvent {
  reason: TripReason;
  detail: string;
  wallDeltaMs: number;
  monoDeltaMs: number;
  tearMs: number;
  revokedLeaseIds: string[];
  trippedAtWallMs: number;
}

export interface GuardianOptions {
  clocks?: ClockPair;
  leaseManager?: LeaseManager;
  /** Max |wallDelta - monoDelta| per tick before tearing is declared. */
  tearToleranceMs?: number;
  /** Max tolerated backward wall-clock movement per tick. */
  backwardToleranceMs?: number;
  /** How long one clock may fail to advance before it is declared stalled. */
  stallThresholdMs?: number;
}

interface Sample {
  monoMs: number;
  wallMs: number;
}

/**
 * Dual-clock drift detector and lease circuit breaker.
 *
 * Every tick it samples both clocks and compares how much each advanced:
 *   tear = wallDelta - monoDelta
 * A healthy pair advances in lockstep (tear ~= 0). Manual time changes, NTP
 * steps and VM clock suspension all produce a large tear or a stalled clock,
 * which trips the breaker: all active leases are force-revoked and the
 * guardian degrades to safe mode, refusing further lease issuance.
 */
export class ClockGuardian extends EventEmitter {
  private readonly clocks: ClockPair;
  private readonly leaseManager: LeaseManager;
  private readonly tearToleranceMs: number;
  private readonly backwardToleranceMs: number;
  private readonly stallThresholdMs: number;

  private prev: Sample | null = null;
  private wallStalledMonoMs = 0;
  private monoStalledWallMs = 0;

  readonly mode: GuardianMode = 'normal';
  private trippedEvent: TripEvent | null = null;

  constructor(options: GuardianOptions = {}) {
    super();
    this.clocks = options.clocks ?? systemClocks();
    this.leaseManager =
      options.leaseManager ?? new LeaseManager(this.clocks.monotonicNow);
    this.tearToleranceMs = options.tearToleranceMs ?? 250;
    this.backwardToleranceMs = options.backwardToleranceMs ?? 1;
    this.stallThresholdMs = options.stallThresholdMs ?? 1_000;
  }

  get leases(): LeaseManager {
    return this.leaseManager;
  }

  get isTripped(): boolean {
    return this.trippedEvent !== null;
  }

  get tripInfo(): TripEvent | null {
    return this.trippedEvent;
  }

  private sample(): Sample {
    return {
      monoMs: this.clocks.monotonicNow(),
      wallMs: this.clocks.wallNow(),
    };
  }

  /** Prime the baseline without evaluating. Call once before the tick loop. */
  start(): void {
    this.prev = this.sample();
  }

  /**
   * Compare one pair of samples. Returns the trip event if the breaker
   * fired on this tick, otherwise null. Once tripped, stays tripped.
   */
  tick(): TripEvent | null {
    if (this.isTripped) return this.trippedEvent;

    const now = this.sample();
    if (this.prev === null) {
      this.prev = now;
      return null;
    }

    const monoDeltaMs = now.monoMs - this.prev.monoMs;
    const wallDeltaMs = now.wallMs - this.prev.wallMs;
    const tearMs = wallDeltaMs - monoDeltaMs;
    this.prev = now;

    // 1. Wall clock moved backwards (manual change / snapshot restore).
    if (wallDeltaMs < -this.backwardToleranceMs) {
      return this.trip({
        reason: 'WALL_CLOCK_BACKWARD',
        detail: `wall clock moved backwards by ${-wallDeltaMs}ms`,
        wallDeltaMs,
        monoDeltaMs,
        tearMs,
      });
    }

    // 2. The two clocks advanced at wildly different paces (NTP step etc.).
    if (Math.abs(tearMs) > this.tearToleranceMs) {
      return this.trip({
        reason: 'CLOCK_TEAR',
        detail:
          `clock tear of ${tearMs}ms exceeds tolerance ` +
          `${this.tearToleranceMs}ms (wall ${wallDeltaMs}ms vs mono ${monoDeltaMs}ms)`,
        wallDeltaMs,
        monoDeltaMs,
        tearMs,
      });
    }

    // 3. One clock stalled while the other kept advancing (VM suspend).
    this.wallStalledMonoMs = wallDeltaMs <= 0 ? this.wallStalledMonoMs + monoDeltaMs : 0;
    this.monoStalledWallMs = monoDeltaMs <= 0 ? this.monoStalledWallMs + wallDeltaMs : 0;

    if (this.wallStalledMonoMs >= this.stallThresholdMs) {
      return this.trip({
        reason: 'WALL_CLOCK_STALLED',
        detail: `wall clock stalled while monotonic advanced ${this.wallStalledMonoMs}ms`,
        wallDeltaMs,
        monoDeltaMs,
        tearMs,
      });
    }
    if (this.monoStalledWallMs >= this.stallThresholdMs) {
      return this.trip({
        reason: 'MONOTONIC_STALLED',
        detail: `monotonic clock stalled while wall advanced ${this.monoStalledWallMs}ms`,
        wallDeltaMs,
        monoDeltaMs,
        tearMs,
      });
    }

    return null;
  }

  private trip(
    partial: Omit<TripEvent, 'revokedLeaseIds' | 'trippedAtWallMs'>,
  ): TripEvent {
    const revoked = this.leaseManager.revokeAll(
      `circuit breaker: ${partial.reason}`,
    );
    this.leaseManager.stopAccepting();

    const event: TripEvent = {
      ...partial,
      revokedLeaseIds: revoked.map((lease) => lease.id),
      trippedAtWallMs: this.clocks.wallNow(),
    };
    this.trippedEvent = event;
    (this as { mode: GuardianMode }).mode = 'safe';
    this.emit('trip', event);
    return event;
  }
}
