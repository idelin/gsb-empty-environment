import { DeterministicRng } from './rng.ts';
import { NetworkStack, TimerService } from './components.ts';
import { compareEvents } from './events.ts';
import type { EventPayload, KernelEvent } from './events.ts';
import { hashState, stateBytes } from './serialize.ts';

export interface SnapshotRecord {
  /** 1-based ordinal of the event that produced this state. */
  index: number;
  tick: number;
  kind: KernelEvent['kind'];
  /** Canonical state bytes — the byte-level replay comparison target. */
  bytes: Buffer;
  /** SHA-256 of `bytes`. */
  hash: string;
}

/**
 * Deterministic event-replay debugging kernel.
 *
 * Recording: every external stimulus (packet arrivals, clock-interrupt
 * timeouts, random-seed injections) is appended to an ordered event log.
 * After each dispatched event the kernel captures a canonical snapshot of
 * every component's memory plus the RNG state.
 *
 * Time travel: jumping to event N resets all components to a pristine
 * state and re-dispatches log[0..N) — no side effects, fully in-memory,
 * and the resulting bytes are bit-identical to the recorded snapshot.
 */
export class Kernel {
  readonly seed: number;
  private clock = 0;
  private rng: DeterministicRng;
  private network = new NetworkStack();
  private timers = new TimerService();

  /** Authoritative event log, in dispatch order. */
  private log: KernelEvent[] = [];
  /** Events injected but not yet dispatched. */
  private pending: Array<EventPayload & { tick: number }> = [];
  /** Cursor into `log`: how many recorded events the live state reflects. */
  private cursor = 0;
  private seqCounter = 0;

  /** Snapshot captured after every dispatched event (index 0 == event 1). */
  readonly snapshots: SnapshotRecord[] = [];

  constructor(seed: number) {
    this.seed = seed >>> 0;
    this.rng = new DeterministicRng(this.seed);
  }

  /** Record an external stimulus. `seq` is assigned in injection order. */
  inject(event: EventPayload & { tick: number }): void {
    this.pending.push(event);
  }

  get eventLog(): readonly KernelEvent[] {
    return this.log;
  }

  get eventCount(): number {
    return this.log.length;
  }

  private captureState(): unknown {
    return {
      clock: this.clock,
      rng: this.rng.snapshot(),
      network: this.network.snapshot(),
      timers: this.timers.snapshot(),
    };
  }

  private dispatch(event: KernelEvent): void {
    this.clock = event.tick;
    switch (event.kind) {
      case 'packet':
        this.network.onPacket(event.payload, this.rng);
        break;
      case 'timeout':
        this.timers.fire(event.payload.timerId);
        this.network.onTimeout(this.rng);
        break;
      case 'reseed':
        this.rng.reseed(event.payload.seed);
        break;
    }
  }

  private takeSnapshot(event: KernelEvent): SnapshotRecord {
    const state = this.captureState();
    return {
      index: this.snapshots.length + 1,
      tick: event.tick,
      kind: event.kind,
      bytes: stateBytes(state),
      hash: hashState(state),
    };
  }

  /** Drain all pending events in deterministic (tick, seq) order. */
  run(): void {
    const ordered: KernelEvent[] = this.pending
      .map((e) => ({ ...e, seq: this.seqCounter++ }))
      .sort(compareEvents);
    this.pending = [];
    for (const event of ordered) {
      this.dispatch(event);
      this.log.push(event);
      this.cursor = this.log.length;
      this.snapshots.push(this.takeSnapshot(event));
    }
  }

  /**
   * Time travel to the state exactly after the `index`-th event
   * (1-based; 0 = pristine initial state). Side-effect-free: rebuilds all
   * components in memory and re-dispatches the recorded log prefix.
   */
  travelTo(index: number): SnapshotRecord {
    if (index < 0 || index > this.log.length) {
      throw new RangeError(`event index ${index} outside log of ${this.log.length}`);
    }
    this.clock = 0;
    this.rng = new DeterministicRng(this.seed);
    this.network = new NetworkStack();
    this.timers = new TimerService();
    this.cursor = 0;
    while (this.cursor < index) {
      this.dispatch(this.log[this.cursor]);
      this.cursor += 1;
    }
    const state = this.captureState();
    return {
      index,
      tick: this.clock,
      kind: index > 0 ? this.log[index - 1].kind : 'reseed',
      bytes: stateBytes(state),
      hash: hashState(state),
    };
  }

  /** Continue dispatching the remaining recorded events after a travel. */
  resume(): void {
    while (this.cursor < this.log.length) {
      this.dispatch(this.log[this.cursor]);
      this.cursor += 1;
    }
  }

  /** Byte-exact comparison of a traveled state against the recorded snapshot. */
  verifyAt(index: number): { match: boolean; expected: string; actual: string } {
    if (index < 1 || index > this.snapshots.length) {
      throw new RangeError(`no snapshot for event ${index}`);
    }
    const traveled = this.travelTo(index);
    const recorded = this.snapshots[index - 1];
    return {
      match: traveled.bytes.equals(recorded.bytes) && traveled.hash === recorded.hash,
      expected: recorded.hash,
      actual: traveled.hash,
    };
  }
}
