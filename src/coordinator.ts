import { ShardServer, TraceFn } from './shard';
import { UnreliableLink } from './link';
import { Key } from './types';

export type Phase = 'copying' | 'draining' | 'done';

/**
 * Drives the handoff from A to B:
 *
 * 1. COPYING   - stream a non-atomic base copy (chunked reads of A's live
 *                store, tombstones included) while concurrently streaming
 *                A's mutation log as deltas. Writes to A remain allowed.
 * 2. FENCE     - once the base copy is fully sent and the log is caught up,
 *                fence A: it stops accepting new writes but still answers
 *                retries of old requests. Record the final sequence number.
 * 3. DRAINING  - ship the remaining log entries; wait until B's contiguous
 *                applied high-water mark reaches A's final sequence.
 * 4. DONE      - activate B with a version counter continuing past A's
 *                final sequence, so post-switchover writes can never
 *                collide with replicated versions.
 */
export class MigrationCoordinator {
  phase: Phase = 'copying';
  finalSeq = -1;
  private baseKeys: Key[] | null = null;
  private baseCursor = 0;
  private logCursor = 0;

  constructor(
    private a: ShardServer,
    private b: ShardServer,
    private link: UnreliableLink,
    private trace: TraceFn,
    private baseChunkSize = 3,
  ) {}

  tick(now: number): void {
    if (this.phase === 'copying') {
      if (this.baseKeys === null) {
        // Only the key list is snapshotted; each record is read at send
        // time, so the base copy is deliberately non-atomic.
        this.baseKeys = this.a.storeKeys();
        this.trace(`[t=${now}] COORD: base copy starts (${this.baseKeys.length} keys)`);
      }
      const end = Math.min(this.baseCursor + this.baseChunkSize, this.baseKeys.length);
      for (; this.baseCursor < end; this.baseCursor++) {
        const key = this.baseKeys[this.baseCursor];
        const rec = this.a.rawRecord(key);
        if (!rec) continue;
        this.link.send(now, {
          channel: 'base',
          mutation: { key, value: rec.value, version: rec.version, requestId: 'base' },
        });
      }
      this.shipDeltas(now);
      if (this.baseCursor >= this.baseKeys.length && this.logCursor >= this.a.opLog.length) {
        this.finalSeq = this.a.fence();
        this.phase = 'draining';
        this.trace(
          `[t=${now}] COORD: *** FENCE *** A stops accepting new writes ` +
            `(finalSeq=${this.finalSeq}); old requests still return cached results`,
        );
      }
    } else if (this.phase === 'draining') {
      this.shipDeltas(now);
      if (this.logCursor >= this.a.opLog.length && this.b.highWater >= this.finalSeq) {
        this.b.activate(this.finalSeq);
        this.phase = 'done';
        this.trace(
          `[t=${now}] COORD: *** SWITCHOVER COMPLETE *** B drained to v${this.finalSeq} and is now ACTIVE`,
        );
      }
    }
  }

  private shipDeltas(now: number): void {
    while (this.logCursor < this.a.opLog.length) {
      this.link.send(now, { channel: 'delta', mutation: this.a.opLog[this.logCursor] });
      this.logCursor++;
    }
  }
}
