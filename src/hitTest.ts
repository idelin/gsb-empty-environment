import type { Affinity, CaretPosition } from './types.ts';
import type { LayoutLine, PositionedRun } from './layout.ts';

/** One visual caret stop: a glyph edge where the caret can rest. */
export interface CaretStop {
  readonly x: number;
  readonly offset: number;
}

/**
 * Every caret stop of the line, derived from the glyph boxes. Each glyph
 * contributes its leading edge (offset `start`) and trailing edge (offset
 * `end`); for RTL glyphs the leading edge is on the visual right.
 */
export function caretStops(line: LayoutLine): CaretStop[] {
  const stops: CaretStop[] = [];
  for (const glyph of line.positions) {
    if (glyph.direction === 'ltr') {
      stops.push({ x: glyph.x, offset: glyph.start });
      stops.push({ x: glyph.x + glyph.width, offset: glyph.end });
    } else {
      stops.push({ x: glyph.x + glyph.width, offset: glyph.start });
      stops.push({ x: glyph.x, offset: glyph.end });
    }
  }
  return stops;
}

/** All visual x positions where a caret for `offset` can be drawn. */
function candidateXs(line: LayoutLine, offset: number): number[] {
  return caretStops(line)
    .filter((stop) => stop.offset === offset)
    .map((stop) => stop.x);
}

interface RunHit {
  readonly offset: number;
  readonly edgeX: number;
  /** Which visual half of the glyph box was hit. */
  readonly half: Affinity;
}

function hitTestRun(run: PositionedRun, x: number): RunHit {
  const glyph = run.glyphs.find(
    (g) => g.width > 0 && x >= g.x && x < g.x + g.width,
  );
  if (glyph === undefined) {
    // X landed on a zero-width glyph or a sub-pixel gap: snap to the
    // nearest run edge.
    const nearLeft = x < run.x + run.width / 2;
    return edgeHit(run, nearLeft ? 'left' : 'right');
  }
  const half: Affinity = (x - glyph.x) * 2 < glyph.width ? 'left' : 'right';
  if (run.direction === 'ltr') {
    return half === 'left'
      ? { offset: glyph.start, edgeX: glyph.x, half }
      : { offset: glyph.end, edgeX: glyph.x + glyph.width, half };
  }
  return half === 'left'
    ? { offset: glyph.end, edgeX: glyph.x, half }
    : { offset: glyph.start, edgeX: glyph.x + glyph.width, half };
}

function edgeHit(run: PositionedRun, side: 'left' | 'right'): RunHit {
  const first = run.glyphs[0];
  const last = run.glyphs[run.glyphs.length - 1];
  if (first === undefined || last === undefined) {
    return { offset: run.logicalStart, edgeX: run.x, half: side };
  }
  if (run.direction === 'ltr') {
    return side === 'left'
      ? { offset: first.start, edgeX: run.x, half: 'left' }
      : { offset: last.end, edgeX: run.x + run.width, half: 'right' };
  }
  return side === 'left'
    ? { offset: last.end, edgeX: run.x, half: 'left' }
    : { offset: first.start, edgeX: run.x + run.width, half: 'right' };
}

/**
 * Map a click x-coordinate to a logical insertion point.
 *
 * Inside a run the glyph under `x` is split at its midpoint; for RTL runs
 * the logical direction is mirrored. At a visual run boundary the side of
 * the boundary that `x` falls on decides which run — and therefore which
 * logical offset — wins. The returned affinity records which of the
 * offset's visual caret positions was hit: `left` for the smaller x,
 * `right` for the larger one.
 */
export function hitTest(line: LayoutLine, x: number): CaretPosition {
  const runs = line.runs.filter((run) => run.glyphs.length > 0);
  if (runs.length === 0) {
    return { offset: 0, affinity: 'left' };
  }

  let hit: RunHit;
  const first = runs[0]!;
  const last = runs[runs.length - 1]!;
  if (x <= line.x) {
    hit = edgeHit(first, 'left');
  } else if (x >= line.x + line.width) {
    hit = edgeHit(last, 'right');
  } else {
    const run = runs.find((r) => x < r.x + r.width) ?? last;
    hit = hitTestRun(run, x);
  }

  const xs = candidateXs(line, hit.offset);
  const min = Math.min(...xs);
  const max = Math.max(...xs);
  const affinity: Affinity =
    min === max ? hit.half : hit.edgeX <= (min + max) / 2 ? 'left' : 'right';
  return { offset: hit.offset, affinity };
}

/**
 * Resolve a caret position back to its visual x. Ambiguous logical offsets
 * (run boundaries) are adjudicated by affinity: `left` yields the smallest
 * candidate x, `right` the largest.
 */
export function caretX(line: LayoutLine, position: CaretPosition): number {
  const xs = candidateXs(line, position.offset);
  if (xs.length === 0) {
    return position.offset <= 0 ? line.x : line.x + line.width;
  }
  return position.affinity === 'left' ? Math.min(...xs) : Math.max(...xs);
}
