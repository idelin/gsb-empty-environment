import type { Message, ReadResult, Snapshot } from './types';
import type { Origin } from './origin';

// Node-level connectivity phase.
export type NodePhase = 'ONLINE' | 'OFFLINE' | 'RESYNCING';

// Per-key serving state:
//   SERVING  - installed version satisfies every known barrier and no
//              invalidation for a newer version is pending.
//   STALE_OK - an invalidation for a newer version arrived, but no barrier
//              covers it yet: reads may still return the old value.
//   BLOCKED  - a barrier beyond the installed version is known: reads must
//              wait for data with version >= barrier (never serve stale).
export type KeyPhase = 'SERVING' | 'STALE_OK' | 'BLOCKED';

export type DelayFn = () => Promise<void>;
export type VoidFn = () => void;

interface KeyState {
  version: number;  // installed data version (0 = nothing installed)
  value: unknown;
  hasValue: boolean;
  barrier: number;  // highest barrier version ever seen (monotonic)
  pending: number;  // highest invalidated version ever seen (0 = none pending)
}

export class ReadTimeoutError extends Error {
  constructor(key: string) {
    super('read timed out waiting for barrier-satisfying data: ' + key);
    this.name = 'ReadTimeoutError';
  }
}

const NO_DELAY: DelayFn = () => Promise.resolve();

export class CacheNode {
  readonly name: string;
  private keys: Map<string, KeyState> = new Map();
  private phase: NodePhase = 'ONLINE';
  private listeners: Array<VoidFn> = [];
  private loadDelay: DelayFn = NO_DELAY;
  private resyncDelay: DelayFn = NO_DELAY;
  private origin: Origin | null = null;

  constructor(name: string) {
    this.name = name;
  }

  // Test/simulator hooks: make the async cache load and the resync
  // artificially slow so races (late loads, reads during resync) are
  // observable deterministically.
  setLoadDelay(d: DelayFn): void {
    this.loadDelay = d;
  }

  setResyncDelay(d: DelayFn): void {
    this.resyncDelay = d;
  }

  attachOrigin(origin: Origin): void {
    this.origin = origin;
  }

  isOnline(): boolean {
    return this.phase === 'ONLINE';
  }

  nodePhase(): NodePhase {
    return this.phase;
  }

  installedVersion(key: string): number {
    return this.stateFor(key).version;
  }

  knownBarrier(key: string): number {
    return this.stateFor(key).barrier;
  }

  keyPhase(key: string): KeyPhase {
    const st = this.stateFor(key);
    if (st.barrier > st.version) return 'BLOCKED';
    if (st.pending > st.version) return 'STALE_OK';
    return 'SERVING';
  }

  // ---- message intake (from the bus) ----

  receive(msg: Message): void {
    if (this.phase !== 'ONLINE') return;
    if (msg.kind === 'invalidate') {
      const st = this.stateFor(msg.key);
      if (msg.version > st.pending) st.pending = msg.version;
      this.notify();
      return;
    }
    if (msg.kind === 'barrier') {
      const st = this.stateFor(msg.key);
      // Monotonic: an old barrier replay can never lower the barrier.
      if (msg.version > st.barrier) st.barrier = msg.version;
      this.notify();
      return;
    }
    // data
    const st = this.stateFor(msg.key);
    if (msg.version > st.version) {
      // Install even when version < barrier: it moves the node forward,
      // but reads stay blocked until version >= barrier is installed.
      st.version = msg.version;
      st.value = msg.value;
      st.hasValue = true;
      if (st.pending <= st.version) st.pending = 0;
      this.notify();
    }
    // stale or duplicate data (version <= installed) is ignored
  }

  // ---- connectivity ----

  disconnect(): void {
    if (this.phase === 'OFFLINE') return;
    this.phase = 'OFFLINE';
    this.notify();
  }

  // Reconnect resyncs from the origin. While RESYNCING, reads must wait:
  // the node knows it may have missed barriers and cannot serve safely.
  async reconnect(): Promise<void> {
    if (this.phase !== 'OFFLINE') return;
    if (!this.origin) throw new Error('reconnect requires an attached origin');
    this.phase = 'RESYNCING';
    this.notify();
    await this.resyncDelay();
    const snaps = this.origin.snapshotAll();
    for (const s of snaps) this.applySnapshot(s);
    this.phase = 'ONLINE';
    this.notify();
  }

  private applySnapshot(s: Snapshot): void {
    const st = this.stateFor(s.key);
    if (s.hasValue && s.version > st.version) {
      st.version = s.version;
      st.value = s.value;
      st.hasValue = true;
    }
    if (s.barrier > st.barrier) st.barrier = s.barrier;
    if (st.pending <= st.version) st.pending = 0;
    this.notify();
  }

  // ---- reads ----
  //
  // A read may only complete with a version >= the barrier known AT
  // COMPLETION TIME. The version is therefore re-validated after the
  // (simulated) async load finishes: a load that started before a barrier
  // arrived but completes after it ("late old load") must not return the
  // stale value; it falls through and waits for satisfying data instead.
  //
  // While OFFLINE the node serves its last known value if its last known
  // barrier allows it (it cannot learn about newer barriers anyway).
  // While RESYNCING reads always wait for the sync to finish.
  async read(key: string, timeoutMs: number = 0): Promise<ReadResult> {
    let deadline = Infinity;
    if (timeoutMs > 0) deadline = Date.now() + timeoutMs;
    for (;;) {
      if (this.phase !== 'RESYNCING') {
        const st = this.stateFor(key);
        if (st.hasValue && st.version >= st.barrier) {
          const version = st.version;
          const value = st.value;
          await this.loadDelay();
          const cur = this.stateFor(key);
          if (version >= cur.barrier && this.phase !== 'RESYNCING') {
            return { value, version };
          }
          // Barrier moved (or resync started) while the load was in
          // flight: do not serve the stale snapshot, wait instead.
          continue;
        }
      }
      const remaining = deadline - Date.now();
      if (remaining <= 0) throw new ReadTimeoutError(key);
      await this.waitForChange(key, remaining);
    }
  }

  // ---- internals ----

  private stateFor(key: string): KeyState {
    let st = this.keys.get(key);
    if (!st) {
      st = { version: 0, value: undefined, hasValue: false, barrier: 0, pending: 0 };
      this.keys.set(key, st);
    }
    return st;
  }

  private notify(): void {
    const ls = this.listeners;
    this.listeners = [];
    for (const l of ls) l();
  }

  private removeListener(l: VoidFn): void {
    const idx = this.listeners.indexOf(l);
    if (idx >= 0) this.listeners.splice(idx, 1);
  }

  private waitForChange(key: string, ms: number): Promise<void> {
    return new Promise((resolve, reject) => {
      let settled = false;
      let timer: ReturnType<typeof setTimeout> | null = null;
      const listener = () => {
        if (settled) return;
        settled = true;
        if (timer !== null) clearTimeout(timer);
        resolve();
      };
      this.listeners.push(listener);
      if (isFinite(ms)) {
        timer = setTimeout(() => {
          if (settled) return;
          settled = true;
          this.removeListener(listener);
          reject(new ReadTimeoutError(key));
        }, ms);
        if (typeof timer !== 'number') timer.unref();
      }
    });
  }
}
