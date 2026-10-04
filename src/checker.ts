import { ShardServer } from './shard';
import { Key, Mutation } from './types';

/**
 * Step-by-step invariant checker. After every simulation step it verifies:
 *
 * 1. No resurrection: B's stored version per key never goes backwards, so
 *    a deleted record can never reappear because of a stale redelivery.
 * 2. Exact payload: every record on B matches the accepted mutation for
 *    its version (right key, right value/tombstone).
 * 3. Eventual visibility: every acknowledged write is tracked; at the end
 *    of the run every one of them must be reflected on B.
 */
export class Checker {
  private knownByVersion = new Map<number, Mutation>(); // accepted anywhere
  private ackedByKey = new Map<Key, Mutation>(); // highest acked version per key
  private ackedCount = 0;
  private bFloor = new Map<Key, number>(); // max version ever observed on B
  readonly violations: string[] = [];

  /** Called when a shard accepts a write (before the response arrives). */
  onAccepted(m: Mutation): void {
    this.knownByVersion.set(m.version, m);
  }

  /** Called when the client receives the success response. */
  onAck(m: Mutation): void {
    this.ackedCount++;
    const cur = this.ackedByKey.get(m.key);
    if (!cur || cur.version < m.version) this.ackedByKey.set(m.key, m);
  }

  get totalAcked(): number {
    return this.ackedCount;
  }

  /** Per-step checks; call after every simulation event. */
  checkStep(t: number, b: ShardServer): void {
    for (const [key, rec] of b.inspect()) {
      const m = this.knownByVersion.get(rec.version);
      if (!m) {
        this.violations.push(
          `[t=${t}] B holds ${key}@v${rec.version} with no corresponding accepted write`,
        );
        continue;
      }
      if (m.key !== key || m.value !== rec.value) {
        this.violations.push(
          `[t=${t}] B holds wrong payload for ${key}@v${rec.version}: ` +
            `expected ${m.key}=${m.value === null ? 'TOMBSTONE' : m.value}`,
        );
      }
      const floor = this.bFloor.get(key);
      if (floor !== undefined && rec.version < floor) {
        this.violations.push(
          `[t=${t}] RESURRECTION on B: ${key} regressed from v${floor} to v${rec.version}` +
            `${rec.value !== null ? ' (deleted record came back to life)' : ''}`,
        );
      }
      if (floor === undefined || rec.version > floor) {
        this.bFloor.set(key, rec.version);
      }
    }
  }

  /** How many acknowledged writes are already visible on B. */
  visibility(b: ShardServer): { visible: number; total: number } {
    const store = b.inspect();
    let visible = 0;
    for (const [key, m] of this.ackedByKey) {
      const rec = store.get(key);
      if (rec && rec.version >= m.version) visible++;
    }
    return { visible, total: this.ackedByKey.size };
  }

  /** Final check: every acknowledged write must be visible on B. */
  finalCheck(b: ShardServer): boolean {
    this.checkStep(Number.MAX_SAFE_INTEGER, b);
    const store = b.inspect();
    for (const [key, m] of this.ackedByKey) {
      const rec = store.get(key);
      if (!rec || rec.version < m.version) {
        this.violations.push(
          `acked write ${m.requestId} (${m.key}@v${m.version}) is NOT visible on B ` +
            `(B has v${rec ? rec.version : 'none'})`,
        );
      }
    }
    return this.violations.length === 0;
  }
}
