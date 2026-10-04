import { MessageBus } from './bus';
import type { Snapshot } from './types';

interface Entry {
  version: number;
  value: unknown;
}

// Origin is the authoritative config store. It bumps versions, publishes
// barriers, and broadcasts invalidations / data / barrier messages.
// Data push is intentionally a separate step from update() so tests can
// exercise "invalidation arrives before data".
export class Origin {
  private bus: MessageBus;
  private store: Map<string, Entry> = new Map();
  private barriers: Map<string, number> = new Map();

  constructor(bus: MessageBus) {
    this.bus = bus;
  }

  // Sets the value and bumps the version without notifying anyone.
  set(key: string, value: unknown): number {
    const cur = this.store.get(key);
    let version = 1;
    if (cur) version = cur.version + 1;
    this.store.set(key, { version, value });
    return version;
  }

  // set() + broadcast an invalidation for the new version.
  update(key: string, value: unknown): number {
    const version = this.set(key, value);
    this.bus.broadcast({ kind: 'invalidate', key, version });
    return version;
  }

  // Broadcasts the data payload for the current version of key.
  pushData(key: string): void {
    const e = this.store.get(key);
    if (!e) throw new Error('unknown key: ' + key);
    this.bus.broadcast({ kind: 'data', key, version: e.version, value: e.value });
  }

  // Publishes the barrier for key at its current version and broadcasts it.
  // Barriers are monotonic: publishing never lowers an existing barrier.
  publishBarrier(key: string): number {
    const e = this.store.get(key);
    if (!e) throw new Error('unknown key: ' + key);
    const cur = this.barriers.get(key) || 0;
    let barrier = cur;
    if (e.version > barrier) barrier = e.version;
    this.barriers.set(key, barrier);
    this.bus.broadcast({ kind: 'barrier', key, version: barrier });
    return barrier;
  }

  barrier(key: string): number {
    return this.barriers.get(key) || 0;
  }

  version(key: string): number {
    const e = this.store.get(key);
    if (!e) return 0;
    return e.version;
  }

  snapshot(key: string): Snapshot {
    const e = this.store.get(key);
    if (!e) {
      return { key, version: 0, value: undefined, hasValue: false, barrier: this.barrier(key) };
    }
    return { key, version: e.version, value: e.value, hasValue: true, barrier: this.barrier(key) };
  }

  snapshotAll(): Array<Snapshot> {
    const keys: Set<string> = new Set();
    for (const k of this.store.keys()) keys.add(k);
    for (const k of this.barriers.keys()) keys.add(k);
    const out: Array<Snapshot> = [];
    for (const k of keys) out.push(this.snapshot(k));
    return out;
  }
}
