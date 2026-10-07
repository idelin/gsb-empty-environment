/**
 * Marker overlay engine.
 *
 * Multiple analyzers attach attributes to half-open ranges [start, end).
 * Ranges are overlaid by priority; for each attribute key the covering
 * marker with the highest priority wins, ties are broken by sourceId
 * (lexicographically larger wins), then by marker id for determinism.
 *
 * Empty ranges (start === end) never cover a character; they are kept
 * purely as anchors and reported separately via `anchors()`.
 *
 * `segments()` emits the minimal non-overlapping partition of the queried
 * span, merging adjacent intervals whose final resolved styles are equal.
 */
export type AttrValue = string | number | boolean;
export type Attrs = Record<string, AttrValue>;

export interface Marker {
  readonly id: string;
  /** Inclusive start offset. */
  readonly start: number;
  /** Exclusive end offset. `start === end` makes the marker an anchor. */
  readonly end: number;
  /** Higher priority wins when ranges overlap. */
  readonly priority: number;
  /** Analyzer identity; arbitrates same-priority conflicts. */
  readonly sourceId: string;
  readonly attrs: Attrs;
}

export interface Segment {
  readonly start: number;
  readonly end: number;
  /** Final resolved style for every character in [start, end). */
  readonly attrs: Attrs;
}

export type MarkerPatch = Partial<Omit<Marker, "id">>;

function isMarker(value: unknown): value is Marker {
  const m = value as Marker;
  return (
    typeof m === "object" &&
    m !== null &&
    typeof m.id === "string" &&
    typeof m.start === "number" &&
    typeof m.end === "number" &&
    typeof m.priority === "number" &&
    typeof m.sourceId === "string" &&
    typeof m.attrs === "object" &&
    m.attrs !== null
  );
}

function validateRange(start: number, end: number): void {
  if (!Number.isInteger(start) || !Number.isInteger(end)) {
    throw new RangeError(`marker offsets must be integers, got [${start}, ${end})`);
  }
  if (start < 0 || end < start) {
    throw new RangeError(`invalid half-open range [${start}, ${end})`);
  }
}

/** Ordering: priority desc, then sourceId desc, then id asc (deterministic). */
function beats(a: Marker, b: Marker): boolean {
  if (a.priority !== b.priority) return a.priority > b.priority;
  if (a.sourceId !== b.sourceId) return a.sourceId > b.sourceId;
  return a.id < b.id;
}

function attrsEqual(a: Attrs, b: Attrs): boolean {
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key) => key in b && a[key] === b[key]);
}

export class MarkerSet {
  readonly #markers = new Map<string, Marker>();

  constructor(markers: readonly Marker[] = []) {
    for (const marker of markers) this.add(marker);
  }

  get size(): number {
    return this.#markers.size;
  }

  has(id: string): boolean {
    return this.#markers.has(id);
  }

  get(id: string): Marker | undefined {
    return this.#markers.get(id);
  }

  add(marker: Marker): void {
    if (!isMarker(marker)) throw new TypeError("invalid marker");
    validateRange(marker.start, marker.end);
    if (this.#markers.has(marker.id)) {
      throw new Error(`duplicate marker id: ${marker.id}`);
    }
    this.#markers.set(
      marker.id,
      Object.freeze({ ...marker, attrs: Object.freeze({ ...marker.attrs }) }),
    );
  }

  /** Replace fields of an existing marker; revalidates the range. */
  update(id: string, patch: MarkerPatch): Marker {
    const current = this.#markers.get(id);
    if (current === undefined) throw new Error(`unknown marker id: ${id}`);
    const next: Marker = { ...current, ...patch, id };
    validateRange(next.start, next.end);
    if (patch.attrs !== undefined) {
      next.attrs = Object.freeze({ ...patch.attrs });
    }
    this.#markers.set(id, Object.freeze(next));
    return next;
  }

  remove(id: string): boolean {
    return this.#markers.delete(id);
  }

  clear(): void {
    this.#markers.clear();
  }

  markers(): Marker[] {
    return [...this.#markers.values()];
  }

  /** Empty ranges inside [from, to]; they anchor positions but cover nothing. */
  anchors(from = 0, to = Number.MAX_SAFE_INTEGER): Marker[] {
    return this.markers()
      .filter((m) => m.start === m.end && m.start >= from && m.start <= to)
      .sort((a, b) => a.start - b.start || (a.id < b.id ? -1 : 1));
  }

  /**
   * Resolve the winning attributes at a single character offset.
   * Anchors never contribute. Returns {} where nothing applies.
   */
  resolveAt(pos: number): Attrs {
    const winners = new Map<string, Marker>();
    for (const marker of this.#markers.values()) {
      if (marker.start > pos || pos >= marker.end) continue;
      for (const key of Object.keys(marker.attrs)) {
        const current = winners.get(key);
        if (current === undefined || beats(marker, current)) {
          winners.set(key, marker);
        }
      }
    }
    const out: Attrs = {};
    for (const [key, marker] of winners) out[key] = marker.attrs[key]!;
    return out;
  }

  /**
   * Minimal non-overlapping segments covering [from, to).
   * Defaults to the full extent of all non-empty markers.
   * Gaps (positions with no attributes) are omitted; adjacent intervals
   * with identical resolved styles are merged into one segment.
   */
  segments(from?: number, to?: number): Segment[] {
    const ranged = this.markers().filter((m) => m.start < m.end);
    if (ranged.length === 0) return [];

    const lo = from ?? Math.min(...ranged.map((m) => m.start));
    const hi = to ?? Math.max(...ranged.map((m) => m.end));
    validateRange(lo, hi);
    if (lo === hi) return [];

    const boundaries = new Set<number>([lo, hi]);
    for (const m of ranged) {
      if (m.start > lo && m.start < hi) boundaries.add(m.start);
      if (m.end > lo && m.end < hi) boundaries.add(m.end);
    }
    const cuts = [...boundaries].sort((a, b) => a - b);

    const segments: Segment[] = [];
    for (let i = 0; i < cuts.length - 1; i++) {
      const start = cuts[i]!;
      const end = cuts[i + 1]!;
      const attrs = this.resolveAt(start);
      if (Object.keys(attrs).length === 0) continue;
      const last = segments[segments.length - 1];
      if (last !== undefined && last.end === start && attrsEqual(last.attrs, attrs)) {
        segments[segments.length - 1] = { start: last.start, end, attrs: last.attrs };
      } else {
        segments.push({ start, end, attrs });
      }
    }
    return segments;
  }
}
