/**
 * marker-overlay: overlays half-open ranges from multiple analyzers and
 * produces minimal disjoint segments with resolved styles.
 *
 * Resolution rules:
 *  - Ranges are half-open: [start, end). A marker covers character offsets
 *    start .. end-1.
 *  - Attributes are resolved per key: among all markers covering a character
 *    and defining key k, the winner is chosen by compareMarkers (see below).
 *  - Empty ranges (start === end) are anchors: they occupy no character and
 *    never influence segments; they are reported separately via anchors().
 */

export type Attrs = Record<string, unknown>;

export interface Marker {
  /** Unique marker id; upsert with the same id replaces the old marker. */
  readonly id: string;
  /** Analyzer / source id, used to arbitrate same-priority conflicts. */
  readonly source: string;
  /** Inclusive start offset (character index, >= 0). */
  readonly start: number;
  /** Exclusive end offset; start === end means an anchor. */
  readonly end: number;
  /** Higher priority wins. */
  readonly priority: number;
  /** Style attributes contributed by this marker. */
  readonly attrs: Attrs;
}

/** A minimal non-overlapping run of text with its final resolved style. */
export interface Segment {
  readonly start: number;
  readonly end: number;
  readonly attrs: Attrs;
}

/** Empty ranges grouped at one offset; they occupy no characters. */
export interface Anchor {
  readonly offset: number;
  /** Strongest first, per compareMarkers. */
  readonly markers: readonly Marker[];
}

/**
 * Total, deterministic ordering used to arbitrate conflicts:
 * higher priority wins; ties are broken by lexicographically greater
 * source id, then by greater marker id (so the order is total).
 */
export function compareMarkers(a: Marker, b: Marker): number {
  if (a.priority !== b.priority) return a.priority < b.priority ? -1 : 1;
  if (a.source !== b.source) return a.source < b.source ? -1 : 1;
  if (a.id !== b.id) return a.id < b.id ? -1 : 1;
  return 0;
}

export class MarkerSet {
  private readonly byId = new Map<string, Marker>();

  get size(): number {
    return this.byId.size;
  }

  has(id: string): boolean {
    return this.byId.has(id);
  }

  get(id: string): Marker | undefined {
    return this.byId.get(id);
  }

  /** Insert a marker, or replace the existing marker with the same id. */
  upsert(marker: Marker): void {
    assertValidMarker(marker);
    this.byId.set(marker.id, marker);
  }

  /** Remove a marker by id; returns true if it existed. */
  remove(id: string): boolean {
    return this.byId.delete(id);
  }

  /** Remove every marker contributed by one analyzer. Returns count removed. */
  removeSource(source: string): number {
    let removed = 0;
    for (const m of this.byId.values()) {
      if (m.source === source) {
        this.byId.delete(m.id);
        removed++;
      }
    }
    return removed;
  }

  clear(): void {
    this.byId.clear();
  }

  markers(): Marker[] {
    return [...this.byId.values()];
  }

  /**
   * Resolved attributes at a single character offset. Empty ranges never
   * contribute. Returns {} when no marker covers the offset.
   */
  resolveAt(offset: number): Attrs {
    return resolveCovering(this.byId.values(), offset);
  }

  /**
   * Minimal disjoint segments over [from, to) with final resolved styles.
   * Adjacent elementary intervals whose resolved attributes are deep-equal
   * are merged into one segment. Gaps (uncovered offsets) produce no segment.
   */
  segments(from = 0, to = Infinity): Segment[] {
    validateWindow(from, to);
    const ranged: Marker[] = [];
    for (const m of this.byId.values()) {
      if (m.start < m.end && m.end > from && m.start < to) ranged.push(m);
    }
    if (ranged.length === 0) return [];

    const bounds = new Set<number>();
    for (const m of ranged) {
      bounds.add(Math.max(m.start, from));
      bounds.add(Math.min(m.end, to));
    }
    const pts = [...bounds].sort((a, b) => a - b);

    const out: Segment[] = [];
    for (let i = 0; i + 1 < pts.length; i++) {
      const s = pts[i];
      const e = pts[i + 1];
      if (s >= e) continue;
      const covered = ranged.some((m) => m.start <= s && s < m.end);
      if (!covered) continue; // gap between ranges produces no segment
      const attrs = resolveCovering(ranged, s);
      const last = out[out.length - 1];
      if (last && last.end === s && deepEqual(last.attrs, attrs)) {
        out[out.length - 1] = { start: last.start, end: e, attrs: last.attrs };
      } else {
        out.push({ start: s, end: e, attrs });
      }
    }
    return out;
  }

  /**
   * Empty ranges (anchors) with offset in [from, to), grouped by offset and
   * ordered by offset. Anchors occupy no characters and never affect segments.
   */
  anchors(from = 0, to = Infinity): Anchor[] {
    validateWindow(from, to);
    const byOffset = new Map<number, Marker[]>();
    for (const m of this.byId.values()) {
      if (m.start === m.end && m.start >= from && m.start < to) {
        const list = byOffset.get(m.start);
        if (list) list.push(m);
        else byOffset.set(m.start, [m]);
      }
    }
    return [...byOffset.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([offset, ms]) => ({
        offset,
        markers: ms.sort((a, b) => compareMarkers(b, a)),
      }));
  }
}

function resolveCovering(markers: Iterable<Marker>, pos: number): Attrs {
  const winners = new Map<string, Marker>();
  for (const m of markers) {
    if (m.start >= m.end) continue; // anchors occupy no characters
    if (pos < m.start || pos >= m.end) continue;
    for (const key of Object.keys(m.attrs)) {
      const current = winners.get(key);
      if (!current || compareMarkers(m, current) > 0) winners.set(key, m);
    }
  }
  const attrs: Attrs = {};
  for (const [key, m] of winners) attrs[key] = m.attrs[key];
  return attrs;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a === 'number' && typeof b === 'number' && Number.isNaN(a) && Number.isNaN(b)) {
    return true;
  }
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  const aArr = Array.isArray(a);
  if (aArr !== Array.isArray(b)) return false;
  if (aArr) {
    const bb = b as readonly unknown[];
    return a.length === bb.length && a.every((v, i) => deepEqual(v, bb[i]));
  }
  const ao = a as Attrs;
  const bo = b as Attrs;
  const keys = Object.keys(ao);
  return (
    keys.length === Object.keys(bo).length &&
    keys.every((k) => Object.prototype.hasOwnProperty.call(bo, k) && deepEqual(ao[k], bo[k]))
  );
}

function validateWindow(from: number, to: number): void {
  if (typeof from !== 'number' || typeof to !== 'number' || Number.isNaN(from) || Number.isNaN(to)) {
    throw new RangeError('window bounds must be numbers');
  }
  if (from < 0) throw new RangeError(`window start must be >= 0, got ${from}`);
  if (from > to) throw new RangeError(`empty window: from (${from}) > to (${to})`);
}

function assertValidMarker(m: Marker): void {
  if (!m || typeof m !== 'object') throw new TypeError('marker must be an object');
  if (typeof m.id !== 'string' || m.id.length === 0) {
    throw new TypeError('marker.id must be a non-empty string');
  }
  if (typeof m.source !== 'string' || m.source.length === 0) {
    throw new TypeError('marker.source must be a non-empty string');
  }
  if (!Number.isInteger(m.start) || !Number.isInteger(m.end)) {
    throw new RangeError(`marker ${m.id}: start/end must be integers`);
  }
  if (m.start < 0) throw new RangeError(`marker ${m.id}: start must be >= 0`);
  if (m.end < m.start) {
    throw new RangeError(`marker ${m.id}: end (${m.end}) < start (${m.start})`);
  }
  if (typeof m.priority !== 'number' || Number.isNaN(m.priority)) {
    throw new TypeError(`marker ${m.id}: priority must be a number`);
  }
  if (typeof m.attrs !== 'object' || m.attrs === null || Array.isArray(m.attrs)) {
    throw new TypeError(`marker ${m.id}: attrs must be a plain object`);
  }
}
