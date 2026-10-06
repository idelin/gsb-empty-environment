import { BLACK, Color, colorEquals } from "./color";
import { Rational } from "./rational";
import { compareRect, isEmpty, Rect, rect } from "./rect";
import { FlatLayer } from "./scene";

/**
 * A maximal rectangle over which the visible layer stack is constant,
 * together with the exact composited result.
 */
export interface Fragment {
  readonly rect: Rect;
  /** Non-premultiplied composited color (BLACK when alpha is 0). */
  readonly color: Color;
  /** Composited coverage in [0, 1]; 1 means fully opaque here. */
  readonly alpha: Rational;
  /** Ids of contributing layers, bottom-to-top, after occlusion pruning. */
  readonly sources: readonly string[];
}

interface CellResult {
  readonly color: Color;
  readonly alpha: Rational;
  readonly sources: readonly string[];
}

/**
 * Composite the layers covering one cell, top-down, pruning as soon as the
 * accumulated transmittance reaches zero (fully occluded from above).
 * Semi-transparent layers never prune the layers below them.
 */
export function compositeCell(layers: readonly FlatLayer[]): CellResult {
  let transmittance = Rational.ONE;
  let accR = Rational.ZERO;
  let accG = Rational.ZERO;
  let accB = Rational.ZERO;
  const sources: string[] = [];
  for (let k = layers.length - 1; k >= 0; k--) {
    const layer = layers[k];
    const a = layer.alpha;
    if (a.isZero()) continue;
    sources.unshift(layer.id);
    accR = accR.add(transmittance.mul(a).mul(layer.color.r));
    accG = accG.add(transmittance.mul(a).mul(layer.color.g));
    accB = accB.add(transmittance.mul(a).mul(layer.color.b));
    transmittance = transmittance.mul(Rational.ONE.sub(a));
    if (transmittance.isZero()) break; // fully occluded: prune everything below
  }
  const alpha = Rational.ONE.sub(transmittance);
  if (alpha.isZero()) return { color: BLACK, alpha, sources };
  return {
    color: { r: accR.div(alpha), g: accG.div(alpha), b: accB.div(alpha) },
    alpha,
    sources,
  };
}

function cellKey(c: CellResult): string {
  return c.sources.join("\n");
}

/**
 * Part 2. Decompose the union of the given layers (bottom-to-top) into
 * pairwise-disjoint rectangular fragments, each carrying the exact rational
 * color composited over a transparent background. Layers below a fully
 * opaque stack are pruned from `sources`; semi-transparent layers keep
 * everything below them visible. Fragments are sorted canonically.
 */
export function compositeLayers(layers: readonly FlatLayer[]): Fragment[] {
  const active = layers.filter((l) => {
    if (l.alpha.compare(Rational.ZERO) < 0 || l.alpha.compare(Rational.ONE) > 0) {
      throw new Error(`compositeLayers: alpha out of [0, 1] for layer "${l.id}"`);
    }
    return !isEmpty(l.rect) && !l.alpha.isZero();
  });
  if (active.length === 0) return [];

  const xs = sortedUnique(active.flatMap((l) => [l.rect.x0, l.rect.x1]));
  const ys = sortedUnique(active.flatMap((l) => [l.rect.y0, l.rect.y1]));

  // Per-cell results; null means no layer covers the cell.
  const rows: (CellResult | null)[][] = [];
  for (let j = 0; j + 1 < ys.length; j++) {
    const row: (CellResult | null)[] = [];
    for (let i = 0; i + 1 < xs.length; i++) {
      const cx0 = xs[i];
      const cx1 = xs[i + 1];
      const cy0 = ys[j];
      const cy1 = ys[j + 1];
      const covering = active.filter(
        (l) =>
          l.rect.x0 <= cx0 &&
          cx1 <= l.rect.x1 &&
          l.rect.y0 <= cy0 &&
          cy1 <= l.rect.y1,
      );
      row.push(covering.length === 0 ? null : compositeCell(covering));
    }
    rows.push(row);
  }

  // Merge cells into maximal rectangles: horizontal runs per row, then
  // extend identical runs vertically.
  const fragments: Fragment[] = [];
  interface Open {
    readonly x0: number;
    readonly x1: number;
    readonly y0: number;
    y1: number;
    readonly cell: CellResult;
  }
  const open = new Map<string, Open>();
  for (let j = 0; j < rows.length; j++) {
    const row = rows[j];
    const seen = new Set<string>();
    let i = 0;
    while (i < row.length) {
      const cell = row[i];
      if (cell === null) {
        i++;
        continue;
      }
      let end = i + 1;
      const key = cellKey(cell);
      while (end < row.length && row[end] !== null && cellKey(row[end]!) === key) {
        end++;
      }
      const runKey = `${xs[i]}|${xs[end]}|${key}`;
      seen.add(runKey);
      const existing = open.get(runKey);
      if (existing !== undefined) {
        existing.y1 = ys[j + 1];
      } else {
        open.set(runKey, {
          x0: xs[i],
          x1: xs[end],
          y0: ys[j],
          y1: ys[j + 1],
          cell,
        });
      }
      i = end;
    }
    for (const [k, o] of open) {
      if (!seen.has(k)) {
        fragments.push(toFragment(o));
        open.delete(k);
      }
    }
  }
  for (const o of open.values()) fragments.push(toFragment(o));

  return fragments.sort((a, b) => compareRect(a.rect, b.rect));
}

function toFragment(o: {
  x0: number;
  x1: number;
  y0: number;
  y1: number;
  cell: CellResult;
}): Fragment {
  return {
    rect: rect(o.x0, o.y0, o.x1, o.y1),
    color: o.cell.color,
    alpha: o.cell.alpha,
    sources: o.cell.sources,
  };
}

function sortedUnique(values: number[]): number[] {
  return [...new Set(values)].sort((a, b) => a - b);
}

/** Convenience: true when two fragments carry identical composited output. */
export function sameAppearance(a: Fragment, b: Fragment): boolean {
  return (
    a.alpha.equals(b.alpha) &&
    colorEquals(a.color, b.color) &&
    a.sources.length === b.sources.length &&
    a.sources.every((s, i) => s === b.sources[i])
  );
}

export { compareRect };
