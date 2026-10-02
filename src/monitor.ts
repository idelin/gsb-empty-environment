import { ShrinkPhaseViolation } from './types.ts';
import type { LockEvent, LockMode, TxState } from './types.ts';

/**
 * ShrinkPhaseManager — the lock-state monitor required by the spec.
 *
 * SS2PL collapses the shrinking phase into a single atomic point at
 * commit/abort. This monitor observes every lock event and enforces:
 *
 *   1. While a transaction is ACTIVE (growing phase) NO lock may be
 *      released — not even a single local row lock.
 *   2. Releases are only legal inside one atomic "shrinking point"
 *      (commit / abort / crash-recovery), and that point must release
 *      ALL locks the transaction holds.
 *
 * Any breach is recorded in `violations` and raises ShrinkPhaseViolation.
 */
export class ShrinkPhaseManager {
  private clock = 0;
  private phases = new Map<number, TxState>();
  private held = new Map<number, Map<string, LockMode>>();
  private inAtomicRelease = new Set<number>();
  private readonly earlyReleaseEvents: LockEvent[] = [];
  readonly events: LockEvent[] = [];
  readonly violations: string[] = [];

  register(txId: number): void {
    this.phases.set(txId, 'ACTIVE');
    this.held.set(txId, new Map());
  }

  setPhase(txId: number, state: TxState): void {
    this.phases.set(txId, state);
  }

  /** Mark the start of the single legal shrinking point for a tx. */
  beginAtomicRelease(txId: number): void {
    this.inAtomicRelease.add(txId);
  }

  /** Mark the end of the shrinking point; verifies nothing is left held. */
  endAtomicRelease(txId: number): void {
    this.inAtomicRelease.delete(txId);
    const remaining = this.held.get(txId);
    if (remaining && remaining.size > 0) {
      this.violations.push(
        `tx${txId}: shrinking point ended but still holds [${[...remaining.keys()].join(', ')}]`,
      );
    }
  }

  onAcquire(txId: number, resource: string, mode: LockMode): void {
    this.events.push({ kind: 'acquire', txId, resource, mode, at: ++this.clock });
    const phase = this.phases.get(txId);
    if (phase !== 'ACTIVE') {
      // 2PL rule: once shrinking has begun, no new locks may be acquired.
      this.violations.push(`tx${txId}: acquired ${mode}(${resource}) while in phase ${phase}`);
    }
    this.held.get(txId)?.set(resource, mode);
  }

  onRelease(txId: number, resource: string, mode: LockMode): void {
    const event: LockEvent = { kind: 'release', txId, resource, mode, at: ++this.clock };
    this.events.push(event);
    if (!this.inAtomicRelease.has(txId)) {
      this.earlyReleaseEvents.push(event);
      const message =
        `tx${txId}: ILLEGAL early release of ${mode}(${resource}) ` +
        `before commit/abort (phase=${this.phases.get(txId)})`;
      this.violations.push(message);
      throw new ShrinkPhaseViolation(message);
    }
    this.held.get(txId)?.delete(resource);
  }

  /** Locks currently held by a transaction (observability for tests/ops). */
  heldLocks(txId: number): ReadonlyMap<string, LockMode> {
    return this.held.get(txId) ?? new Map();
  }

  /** True when the tx is in its growing phase and must hold every lock. */
  isGrowing(txId: number): boolean {
    return this.phases.get(txId) === 'ACTIVE';
  }

  /** Release events that happened outside any atomic shrinking point. */
  earlyReleases(): LockEvent[] {
    return [...this.earlyReleaseEvents];
  }

  verify(): boolean {
    return this.violations.length === 0;
  }
}
