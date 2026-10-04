import {
  alertIdOf,
  type ApplyResult,
  type Change,
  type EventRevision,
  type EventType,
  type MatcherOptions,
} from './types.ts';

interface StoredRecord {
  id: string;
  revision: number;
  type: EventType;
  timestamp: number;
  retracted: boolean;
  contentKey: string;
}

export interface OpenPair {
  alertId: string;
  aId: string;
  bId: string;
  tA: number;
  tB: number;
}

interface SnapshotData {
  version: 1;
  windowMs: number;
  allowedLateness: number;
  watermark: number;
  seq: number;
  archivedCount: number;
  records: StoredRecord[];
  openPairs: OpenPair[];
  prunedRevisions: Array<[string, number]>;
}

function contentKeyOf(rev: EventRevision): string {
  return `${rev.type}|${rev.timestamp}|${rev.retracted === true ? 1 : 0}`;
}

function compareKey(tsA: number, idA: string, tsB: number, idB: string): number {
  if (tsA !== tsB) return tsA - tsB;
  return idA < idB ? -1 : idA > idB ? 1 : 0;
}

/** First index i with (ts[i], id[i]) >= (ts, id). */
function lowerBound(live: StoredRecord[], ts: number, id: string): number {
  let lo = 0;
  let hi = live.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    const rec = live[mid];
    if (compareKey(rec.timestamp, rec.id, ts, id) < 0) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

const MIN_ID = '';
const MAX_ID = '￿';

/**
 * Streaming bounded pattern matcher.
 *
 * Pattern: an A event followed by a B event with `tB - tA <= windowMs`
 * (both window ends inclusive) and no C event with `tA <= tC <= tB`
 * (inclusive: a C sharing an endpoint's timestamp also invalidates).
 * Events at the same timestamp are ordered by event id.
 *
 * Output: an ordered change stream of upsert/retract per (aId, bId) pair;
 * the alert id is derived from the pair and is stable across revisions.
 */
export class PatternMatcher {
  private readonly windowMs: number;
  private readonly allowedLateness: number;
  private watermark = Number.NEGATIVE_INFINITY;
  /** Latest record per event id, including retracted tombstones. */
  private records = new Map<string, StoredRecord>();
  /**
   * Highest revision seen for ids whose records were pruned by the
   * watermark. Re-deliveries at or below it are stale; anything newer is
   * a revision of already-closed state and is rejected as late. This is
   * what makes archiving safe while keeping memory bounded.
   */
  private prunedRevisions = new Map<string, number>();
  /** Live (non-retracted) records sorted by (timestamp, id). */
  private live: StoredRecord[] = [];
  /** Currently valid (open) alerts keyed by alert id. */
  private openPairs = new Map<string, OpenPair>();
  private seq = 0;
  private archivedCount = 0;
  private readonly onArchive?: (pair: OpenPair) => void;
  readonly conflicts: Array<{ id: string; revision: number }> = [];

  constructor(options: MatcherOptions, onArchive?: (pair: OpenPair) => void) {
    if (options.windowMs < 0) throw new Error('windowMs must be >= 0');
    if (options.allowedLateness < 0) throw new Error('allowedLateness must be >= 0');
    this.windowMs = options.windowMs;
    this.allowedLateness = options.allowedLateness;
    this.onArchive = onArchive;
  }

  get currentWatermark(): number {
    return this.watermark;
  }

  get openAlertCount(): number {
    return this.openPairs.size;
  }

  get archivedAlertCount(): number {
    return this.archivedCount;
  }

  get liveEventCount(): number {
    return this.live.length;
  }

  /** Current open alerts as a map alertId -> {aId, bId}. */
  openAlerts(): Map<string, { aId: string; bId: string }> {
    const out = new Map<string, { aId: string; bId: string }>();
    for (const [id, p] of this.openPairs) out.set(id, { aId: p.aId, bId: p.bId });
    return out;
  }

  /** Live (non-retracted) events at their latest accepted revision. */
  liveEvents(): EventRevision[] {
    return this.live.map((r) => ({
      id: r.id,
      revision: r.revision,
      type: r.type,
      timestamp: r.timestamp,
    }));
  }

  /**
   * Apply one revision. Revisions are evaluated per event id:
   * higher revision wins, identical re-delivery is a no-op, same revision
   * with different content is a conflict (rejected), and anything beyond
   * the allowed lateness is rejected without touching state.
   */
  apply(rev: EventRevision): ApplyResult {
    const existing = this.records.get(rev.id);
    const contentKey = contentKeyOf(rev);
    if (existing === undefined) {
      const prunedRev = this.prunedRevisions.get(rev.id);
      if (prunedRev !== undefined) {
        return { status: rev.revision <= prunedRev ? 'stale' : 'late', changes: [] };
      }
    }
    if (existing !== undefined) {
      if (rev.revision < existing.revision) return { status: 'stale', changes: [] };
      if (rev.revision === existing.revision) {
        if (existing.contentKey === contentKey) return { status: 'duplicate', changes: [] };
        this.conflicts.push({ id: rev.id, revision: rev.revision });
        return { status: 'conflict', changes: [] };
      }
    }
    const floor = this.watermark - this.allowedLateness;
    if (rev.timestamp < floor) return { status: 'late', changes: [] };

    const retracted = rev.retracted === true;
    const affectedTimes: number[] = [];
    if (existing !== undefined && !existing.retracted) {
      this.removeLive(existing);
      affectedTimes.push(existing.timestamp);
    }
    const record: StoredRecord = {
      id: rev.id,
      revision: rev.revision,
      type: rev.type,
      timestamp: rev.timestamp,
      retracted,
      contentKey,
    };
    this.records.set(rev.id, record);
    if (!retracted) {
      this.insertLive(record);
      affectedTimes.push(rev.timestamp);
    }
    const changes: Change[] = [];
    for (const t of affectedTimes) this.recomputeAround(t, changes);
    return { status: 'applied', changes };
  }

  /** Convenience batch wrapper; returns the concatenated change stream. */
  applyAll(revisions: Iterable<EventRevision>): Change[] {
    const out: Change[] = [];
    for (const rev of revisions) out.push(...this.apply(rev).changes);
    return out;
  }

  /**
   * Advance the watermark (monotonic; non-increasing values are ignored).
   * Events and pairs that can no longer be affected by any acceptable
   * revision are pruned / archived.
   */
  advanceWatermark(ts: number): { prunedEvents: number; archivedPairs: number } {
    if (ts <= this.watermark) return { prunedEvents: 0, archivedPairs: 0 };
    this.watermark = ts;
    const floor = ts - this.allowedLateness;
    // A live event can still join a new pair only if its timestamp is
    // >= floor - windowMs (as the A endpoint of a pair whose B arrives at
    // the lateness floor, or as a C inside such a pair's interval).
    const pruneBefore = floor - this.windowMs;

    const cut = lowerBound(this.live, pruneBefore, MIN_ID);
    if (cut > 0) {
      const removed = this.live.splice(0, cut);
      for (const rec of removed) {
        this.records.delete(rec.id);
        this.prunedRevisions.set(rec.id, rec.revision);
      }
    }
    // Prune retracted tombstones past the horizon as well.
    let prunedEvents = cut;
    for (const [id, rec] of this.records) {
      if (rec.retracted && rec.timestamp < pruneBefore) {
        this.records.delete(id);
        this.prunedRevisions.set(id, rec.revision);
        prunedEvents += 1;
      }
    }
    // A pair is closed once both endpoints are prunable; then no accepted
    // revision (of A, B, or any C in its interval) can still reach it.
    let archivedPairs = 0;
    for (const [id, pair] of this.openPairs) {
      if (pair.tB < pruneBefore) {
        this.deletePair(id);
        this.archivedCount += 1;
        archivedPairs += 1;
        if (this.onArchive !== undefined) this.onArchive(pair);
      }
    }
    return { prunedEvents, archivedPairs };
  }

  /** Serialize the full operator state for checkpointing. */
  snapshot(): string {
    const data: SnapshotData = {
      version: 1,
      windowMs: this.windowMs,
      allowedLateness: this.allowedLateness,
      watermark: this.watermark,
      seq: this.seq,
      archivedCount: this.archivedCount,
      records: [...this.records.values()],
      openPairs: [...this.openPairs.values()],
      prunedRevisions: [...this.prunedRevisions.entries()],
    };
    return JSON.stringify(data);
  }

  /** Restore an operator from a snapshot produced by `snapshot()`. */
  static restore(json: string, onArchive?: (pair: OpenPair) => void): PatternMatcher {
    const data = JSON.parse(json) as SnapshotData;
    if (data.version !== 1) throw new Error(`unsupported snapshot version ${data.version}`);
    const matcher = new PatternMatcher(
      { windowMs: data.windowMs, allowedLateness: data.allowedLateness },
      onArchive,
    );
    matcher.watermark = data.watermark;
    matcher.seq = data.seq;
    matcher.archivedCount = data.archivedCount;
    matcher.records = new Map(data.records.map((r) => [r.id, r]));
    matcher.prunedRevisions = new Map(data.prunedRevisions ?? []);
    matcher.live = data.records
      .filter((r) => !r.retracted)
      .sort((a, b) => compareKey(a.timestamp, a.id, b.timestamp, b.id));
    matcher.openPairs = new Map(data.openPairs.map((p) => [p.alertId, p]));
    return matcher;
  }

  private insertLive(rec: StoredRecord): void {
    const idx = lowerBound(this.live, rec.timestamp, rec.id);
    this.live.splice(idx, 0, rec);
  }

  private removeLive(rec: StoredRecord): void {
    const idx = lowerBound(this.live, rec.timestamp, rec.id);
    if (idx < this.live.length && this.live[idx].id === rec.id) {
      this.live.splice(idx, 1);
    }
  }

  private addPair(pair: OpenPair): void {
    this.openPairs.set(pair.alertId, pair);
  }

  private deletePair(alertId: string): void {
    this.openPairs.delete(alertId);
  }

  private emit(changes: Change[], kind: Change['kind'], pair: OpenPair): void {
    this.seq += 1;
    changes.push({ seq: this.seq, kind, alertId: pair.alertId, aId: pair.aId, bId: pair.bId });
  }

  /**
   * Recompute every pair whose A endpoint lies in [t - W, t]; any change
   * at time t (A/B/C insert or removal) can only affect pairs spanning t,
   * and those pairs start no earlier than t - W.
   */
  private recomputeAround(t: number, changes: Change[]): void {
    const lo = t - this.windowMs;
    const hi = t;
    const fresh = this.computePairsInRange(lo, hi);

    // Existing pairs whose A endpoint lies in the affected range. Scanning
    // openPairs (not the live index) also catches pairs whose A was just
    // retracted or revised away.
    const staleIds: string[] = [];
    for (const [alertId, pair] of this.openPairs) {
      if (pair.tA >= lo && pair.tA <= hi && !fresh.has(alertId)) staleIds.push(alertId);
    }
    for (const alertId of staleIds) {
      const pair = this.openPairs.get(alertId);
      if (pair === undefined) continue;
      this.deletePair(alertId);
      this.emit(changes, 'retract', pair);
    }
    for (const [alertId, pair] of fresh) {
      if (this.openPairs.has(alertId)) continue;
      this.addPair(pair);
      this.emit(changes, 'upsert', pair);
    }
  }

  /** All valid pairs with tA in [lo, hi], computed from the live index. */
  private computePairsInRange(lo: number, hi: number): Map<string, OpenPair> {
    const out = new Map<string, OpenPair>();
    const startIdx = lowerBound(this.live, lo, MIN_ID);
    const endIdx = lowerBound(this.live, hi + this.windowMs, MAX_ID);
    const slice = this.live.slice(startIdx, endIdx);
    // Prefix counts of C events over the slice for O(log n) blockage checks.
    const cPrefix = new Array<number>(slice.length + 1).fill(0);
    for (let i = 0; i < slice.length; i += 1) {
      cPrefix[i + 1] = cPrefix[i] + (slice[i].type === 'C' ? 1 : 0);
    }
    const countC = (fromTs: number, toTs: number): number => {
      const from = lowerBound(slice, fromTs, MIN_ID);
      const to = lowerBound(slice, toTs, MAX_ID);
      return cPrefix[to] - cPrefix[from];
    };
    for (let i = 0; i < slice.length; i += 1) {
      const a = slice[i];
      if (a.type !== 'A' || a.timestamp < lo || a.timestamp > hi) continue;
      // B may share A's timestamp (window is inclusive); scan from the
      // first slot with ts >= tA since a same-tick B with a smaller id
      // sorts before A.
      const jStart = lowerBound(slice, a.timestamp, MIN_ID);
      for (let j = jStart; j < slice.length; j += 1) {
        const b = slice[j];
        if (b.timestamp > a.timestamp + this.windowMs) break;
        if (b.type !== 'B') continue;
        if (countC(a.timestamp, b.timestamp) !== 0) continue;
        const alertId = alertIdOf(a.id, b.id);
        out.set(alertId, { alertId, aId: a.id, bId: b.id, tA: a.timestamp, tB: b.timestamp });
      }
    }
    return out;
  }
}
