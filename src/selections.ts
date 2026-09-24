/**
 * Multi-selection state mapped across versions.
 *
 * Anchor and active ends are stored separately, each with its own affinity
 * (zero-width stickiness). Direction is explicit and survives mapping when a
 * selection is (or becomes) zero-width. Deletions that make selections touch
 * merge them into the primary (lowest-id) selection; every swallowed
 * selection is recorded as a `SelectionSource` (id, direction, endpoints with
 * stickiness) so reverse mapping can report exactly what was lost and branch
 * undo can restore it.
 */

import { Mapping, type Affinity, type AmbiguitySource } from './mapping.ts';

export type Direction = 'forward' | 'backward';
export type SelectionEnd = 'anchor' | 'active';

export interface Endpoint {
  /** UTF-16 offset. */
  readonly offset: number;
  /** Zero-width stickiness towards adjacent text. */
  readonly affinity: Affinity;
}

/** Origin record of a selection that was merged away by a deletion. */
export interface SelectionSource {
  readonly id: number;
  readonly direction: Direction;
  readonly anchor: Endpoint;
  readonly active: Endpoint;
}

export class Selection {
  readonly id: number;
  readonly anchor: Endpoint;
  readonly active: Endpoint;
  readonly direction: Direction;
  /** Selections swallowed by merges, newest last (flattened). */
  readonly sources: readonly SelectionSource[];

  constructor(
    id: number,
    anchor: Endpoint,
    active: Endpoint,
    direction: Direction,
    sources: readonly SelectionSource[] = [],
  ) {
    this.id = id;
    this.anchor = anchor;
    this.active = active;
    this.direction = direction;
    this.sources = sources;
  }

  static create(
    id: number,
    anchorOffset: number,
    activeOffset: number,
    anchorAffinity: Affinity = 'after',
    activeAffinity: Affinity = 'before',
  ): Selection {
    const direction: Direction = activeOffset < anchorOffset ? 'backward' : 'forward';
    return new Selection(
      id,
      { offset: anchorOffset, affinity: anchorAffinity },
      { offset: activeOffset, affinity: activeAffinity },
      direction,
    );
  }

  static cursor(id: number, offset: number, affinity: Affinity = 'after'): Selection {
    return new Selection(id, { offset, affinity }, { offset, affinity }, 'forward');
  }

  get isCursor(): boolean {
    return this.anchor.offset === this.active.offset;
  }

  get from(): number {
    return Math.min(this.anchor.offset, this.active.offset);
  }

  get to(): number {
    return Math.max(this.anchor.offset, this.active.offset);
  }
}

/** One merge caused by mapping (typically a deletion collapsing selections together). */
export interface MergeEvent {
  /** Surviving primary selection id. */
  readonly into: number;
  /** Merged result range in the target version. */
  readonly from: number;
  readonly to: number;
  /** Lost selections with id, direction and zero-width stickiness. */
  readonly sources: readonly SelectionSource[];
}

export type SelectionAmbiguity =
  | {
      readonly kind: 'offset';
      readonly selectionId: number;
      readonly end: SelectionEnd;
      readonly sources: readonly AmbiguitySource[];
    }
  | {
      readonly kind: 'merged-sources';
      readonly selectionId: number;
      readonly lost: readonly SelectionSource[];
    };

function mapEndpointForward(ep: Endpoint, mapping: Mapping): Endpoint {
  return { offset: mapping.mapOffset(ep.offset, ep.affinity), affinity: ep.affinity };
}

function directionOf(anchor: number, active: number, fallback: Direction): Direction {
  if (active > anchor) return 'forward';
  if (active < anchor) return 'backward';
  return fallback;
}

function mapSelectionForward(sel: Selection, mapping: Mapping): Selection {
  const anchor = mapEndpointForward(sel.anchor, mapping);
  const active = mapEndpointForward(sel.active, mapping);
  return new Selection(
    sel.id,
    anchor,
    active,
    directionOf(anchor.offset, active.offset, sel.direction),
    sel.sources,
  );
}

function toSource(sel: Selection): SelectionSource {
  return { id: sel.id, direction: sel.direction, anchor: sel.anchor, active: sel.active };
}

interface MappedPair {
  /** Selection as it was in the source version (origin of source records). */
  readonly orig: Selection;
  /** Selection mapped into the target version. */
  readonly mapped: Selection;
}

/**
 * Merge a group of touching selections, keeping the primary (lowest id)
 * identity. Source records capture the pre-mapping endpoints, so a lost
 * selection's id, direction and zero-width stickiness refer to the version
 * where it was last alive.
 */
function mergeGroup(group: MappedPair[]): { merged: Selection; event: MergeEvent } {
  const sorted = group.slice().sort((a, b) => a.orig.id - b.orig.id);
  const primary = sorted[0];
  const lo = Math.min(...sorted.map((p) => p.mapped.from));
  const hi = Math.max(...sorted.map((p) => p.mapped.to));

  const affinityAt = (offset: number, end: SelectionEnd): Affinity => {
    for (const p of sorted) {
      if (p.mapped.anchor.offset === offset && end === 'anchor') return p.mapped.anchor.affinity;
      if (p.mapped.active.offset === offset && end === 'active') return p.mapped.active.affinity;
    }
    for (const p of sorted) {
      if (p.mapped.anchor.offset === offset) return p.mapped.anchor.affinity;
      if (p.mapped.active.offset === offset) return p.mapped.active.affinity;
    }
    return end === 'anchor' ? 'after' : 'before';
  };

  const anchorEp: Endpoint = { offset: lo, affinity: affinityAt(lo, 'anchor') };
  const activeEp: Endpoint = { offset: hi, affinity: affinityAt(hi, 'active') };
  const direction = primary.mapped.direction;
  const anchor = direction === 'forward' ? anchorEp : activeEp;
  const active = direction === 'forward' ? activeEp : anchorEp;

  const lost: SelectionSource[] = [];
  for (const p of sorted) {
    if (p.orig.id === primary.orig.id) {
      lost.push(...p.orig.sources);
    } else {
      lost.push(...p.orig.sources, toSource(p.orig));
    }
  }
  const merged = new Selection(primary.orig.id, anchor, active, direction, lost);
  return { merged, event: { into: primary.orig.id, from: lo, to: hi, sources: lost } };
}

export class SelectionSet {
  readonly selections: readonly Selection[];

  constructor(selections: readonly Selection[]) {
    this.selections = selections;
  }

  static of(...selections: Selection[]): SelectionSet {
    return new SelectionSet(selections);
  }

  get(id: number): Selection | undefined {
    return this.selections.find((s) => s.id === id);
  }

  /**
   * Map all selections to the mapping's target version. Selections that end
   * up touching (e.g. after a deletion removed the text between them, or
   * crushed cursors onto one point) merge into the primary selection.
   */
  mapForward(mapping: Mapping): { set: SelectionSet; merges: MergeEvent[] } {
    const pairs: MappedPair[] = this.selections.map((s) => ({
      orig: s,
      mapped: mapSelectionForward(s, mapping),
    }));
    const order = pairs
      .slice()
      .sort((a, b) => a.mapped.from - b.mapped.from || a.mapped.to - b.mapped.to || a.orig.id - b.orig.id);

    const kept: Selection[] = [];
    const merges: MergeEvent[] = [];
    let group: MappedPair[] = [];
    let groupHi = -1;

    const flush = () => {
      if (group.length === 0) return;
      if (group.length === 1) {
        kept.push(group[0].mapped);
      } else {
        const { merged, event } = mergeGroup(group);
        kept.push(merged);
        merges.push(event);
      }
      group = [];
    };

    for (const pair of order) {
      if (group.length > 0 && pair.mapped.from <= groupHi) {
        group.push(pair);
        groupHi = Math.max(groupHi, pair.mapped.to);
      } else {
        flush();
        group = [pair];
        groupHi = pair.mapped.to;
      }
    }
    flush();
    kept.sort((a, b) => a.id - b.id);
    return { set: new SelectionSet(kept), merges };
  }

  /**
   * Map all selections back to the mapping's source version. Information
   * destroyed by the forward direction (deleted content, merged-away
   * selections) is reported as ambiguity instead of being silently invented.
   */
  mapBackward(mapping: Mapping): { set: SelectionSet; ambiguities: SelectionAmbiguity[] } {
    const ambiguities: SelectionAmbiguity[] = [];
    const mapped = this.selections.map((sel) => {
      const a = mapping.mapOffsetBack(sel.anchor.offset, sel.anchor.affinity);
      const c = mapping.mapOffsetBack(sel.active.offset, sel.active.affinity);
      if (a.ambiguous) {
        ambiguities.push({ kind: 'offset', selectionId: sel.id, end: 'anchor', sources: a.sources });
      }
      if (c.ambiguous) {
        ambiguities.push({ kind: 'offset', selectionId: sel.id, end: 'active', sources: c.sources });
      }
      if (sel.sources.length > 0) {
        ambiguities.push({ kind: 'merged-sources', selectionId: sel.id, lost: sel.sources });
      }
      return new Selection(
        sel.id,
        { offset: a.offset, affinity: sel.anchor.affinity },
        { offset: c.offset, affinity: sel.active.affinity },
        directionOf(a.offset, c.offset, sel.direction),
        sel.sources,
      );
    });
    return { set: new SelectionSet(mapped), ambiguities };
  }
}
