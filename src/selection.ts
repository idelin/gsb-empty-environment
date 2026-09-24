/**
 * Multi-selection mapping across versions.
 *
 * A selection keeps its anchor end and active end as independent endpoints,
 * each with its own stickiness, so direction is a derived property that is
 * preserved structurally (a reverse selection stays reverse after edits in
 * front of it). When an edit (typically a deletion) presses several
 * selections onto the same range, they merge: the primary selection keeps
 * its identity and the absorbed ones are recorded as provenance
 * (`mergedFrom`) with source id, direction and zero-width stickiness, so a
 * later undo can restore them. Reverse mapping without such provenance
 * reports ambiguity instead of guessing.
 */

import type { Version, VersionId } from './model.ts';
import {
  hasLostContext,
  invertMapping,
  mapBoundary,
  type Mapping,
  type Stickiness,
} from './mapping.ts';

export type { Stickiness } from './mapping.ts';

export type Direction = 'forward' | 'backward' | 'collapsed';

export interface Endpoint {
  boundary: number;
  stick: Stickiness;
}

export interface SelectionSource {
  id: number;
  direction: Direction;
  anchor: Endpoint;
  active: Endpoint;
}

export interface MergeInfo {
  sourceVersion: VersionId;
  /** All original selections, primary first, in source-version coordinates. */
  sources: SelectionSource[];
}

export interface Selection {
  id: number;
  anchor: Endpoint;
  active: Endpoint;
  mergedFrom?: MergeInfo;
}

export function directionOf(sel: Pick<Selection, 'anchor' | 'active'>): Direction {
  if (sel.active.boundary > sel.anchor.boundary) return 'forward';
  if (sel.active.boundary < sel.anchor.boundary) return 'backward';
  return 'collapsed';
}

function sourceOf(sel: Selection): SelectionSource {
  return {
    id: sel.id,
    direction: directionOf(sel),
    anchor: { ...sel.anchor },
    active: { ...sel.active },
  };
}

// ---------------------------------------------------------------------------
// UTF-16 public surface
// ---------------------------------------------------------------------------

/** UTF-16 offset of a boundary; boundaries never fall inside a surrogate pair. */
export function boundaryToUtf16(version: Version, boundary: number): number {
  const b = Math.max(0, Math.min(boundary, version.chars.length));
  let offset = 0;
  for (let i = 0; i < b; i++) offset += version.chars[i].ch.length;
  return offset;
}

/**
 * Boundary for a UTF-16 offset. An offset that would split a surrogate pair
 * snaps down to the pair's start, so the public API can never cut a pair.
 */
export function utf16ToBoundary(version: Version, offset: number): number {
  if (offset <= 0) return 0;
  let acc = 0;
  const chars = version.chars;
  for (let i = 0; i < chars.length; i++) {
    const next = acc + chars[i].ch.length;
    if (offset < next) return i;
    acc = next;
  }
  return chars.length;
}

export function selectionFromUtf16(
  version: Version,
  id: number,
  anchorOffset: number,
  activeOffset: number,
  anchorStick: Stickiness = 'left',
  activeStick: Stickiness = 'right',
): Selection {
  return {
    id,
    anchor: { boundary: utf16ToBoundary(version, anchorOffset), stick: anchorStick },
    active: { boundary: utf16ToBoundary(version, activeOffset), stick: activeStick },
  };
}

export function selectionToUtf16(
  version: Version,
  sel: Selection,
): { anchor: number; active: number } {
  return {
    anchor: boundaryToUtf16(version, sel.anchor.boundary),
    active: boundaryToUtf16(version, sel.active.boundary),
  };
}

// ---------------------------------------------------------------------------
// Forward mapping and merging
// ---------------------------------------------------------------------------

export function mapSelection(sel: Selection, m: Mapping): Selection {
  return {
    id: sel.id,
    anchor: {
      boundary: mapBoundary(m, sel.anchor.boundary, sel.anchor.stick),
      stick: sel.anchor.stick,
    },
    active: {
      boundary: mapBoundary(m, sel.active.boundary, sel.active.stick),
      stick: sel.active.stick,
    },
  };
}

export interface MergeReport {
  keptId: number;
  absorbedIds: number[];
}

export interface ForwardResult {
  /** Visible selections in the target version (merges already applied). */
  selections: Selection[];
  merges: MergeReport[];
}

/**
 * Map a set of selections forward through `m`. Selections pressed onto the
 * same range merge into one visible selection: the primary (first in input
 * order) keeps its identity, the absorbed ones are stored in `mergedFrom`
 * with their id, direction and stickiness.
 */
export function mapSelectionsForward(selections: Selection[], m: Mapping): ForwardResult {
  const out: Selection[] = [];
  const originals: Selection[] = [];
  const byRange = new Map<string, number>();
  const merges: MergeReport[] = [];

  for (const original of selections) {
    const mapped = mapSelection(original, m);
    const key = `${mapped.anchor.boundary}->${mapped.active.boundary}`;
    const existing = byRange.get(key);
    if (existing === undefined) {
      byRange.set(key, out.length);
      out.push(mapped);
      originals.push(original);
      continue;
    }
    const kept = out[existing];
    if (!kept.mergedFrom || kept.mergedFrom.sourceVersion !== m.from) {
      kept.mergedFrom = { sourceVersion: m.from, sources: [sourceOf(originals[existing])] };
    }
    kept.mergedFrom.sources.push(sourceOf(original));
    let report = merges.find((r) => r.keptId === kept.id);
    if (!report) {
      report = { keptId: kept.id, absorbedIds: [] };
      merges.push(report);
    }
    report.absorbedIds.push(original.id);
  }
  return { selections: out, merges };
}

// ---------------------------------------------------------------------------
// Reverse mapping, ambiguity and undo
// ---------------------------------------------------------------------------

export interface LostContext {
  selectionId: number;
  endpoint: 'anchor' | 'active';
  boundary: number;
}

export interface ReverseResult {
  /** Restored selections in the source version (several when a merge is undone). */
  selections: Selection[];
  ambiguous: boolean;
  /** Human-readable ambiguity sources. */
  ambiguity: string[];
  /** Structured ambiguity detail. */
  lostContext: LostContext[];
}

/**
 * Map a selection back through `m` (which maps source -> target; the
 * selection lives in the target). If the selection carries merge provenance
 * for exactly this source version, the original selections are restored
 * deterministically (undo). Otherwise the inverse mapping is used and any
 * unrecoverable information is reported as ambiguity.
 */
export function mapSelectionBack(sel: Selection, m: Mapping): ReverseResult {
  if (sel.mergedFrom && sel.mergedFrom.sourceVersion === m.from) {
    const selections = sel.mergedFrom.sources.map((s) => ({
      id: s.id,
      anchor: { ...s.anchor },
      active: { ...s.active },
    }));
    return { selections, ambiguous: false, ambiguity: [], lostContext: [] };
  }

  const inv = invertMapping(m);
  const anchor: Endpoint = {
    boundary: mapBoundary(inv, sel.anchor.boundary, sel.anchor.stick),
    stick: sel.anchor.stick,
  };
  const active: Endpoint = {
    boundary: mapBoundary(inv, sel.active.boundary, sel.active.stick),
    stick: sel.active.stick,
  };

  const ambiguity: string[] = [];
  const lostContext: LostContext[] = [];
  for (const [name, ep] of [['anchor', anchor], ['active', active]] as const) {
    if (hasLostContext(inv, ep.boundary)) {
      ambiguity.push(
        `selection ${sel.id} ${name} at boundary ${ep.boundary} is adjacent to text deleted between v${m.from} and v${m.to}; exact source position is not recoverable`,
      );
      lostContext.push({ selectionId: sel.id, endpoint: name, boundary: ep.boundary });
    }
  }
  return {
    selections: [{ id: sel.id, anchor, active }],
    ambiguous: ambiguity.length > 0,
    ambiguity,
    lostContext,
  };
}
