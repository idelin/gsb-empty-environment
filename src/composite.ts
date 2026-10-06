import { Rect, rect, compareRects } from "./rect";
import {
  PremulColor,
  StraightColor,
  TRANSPARENT,
  over,
  scale,
  toPremul,
  toStraight,
  premulKey,
} from "./color";
import { Scene, SceneNode } from "./scene";

/**
 * A maximal rectangular region over which the composited color is constant.
 * Bounds are half-open. `color` is the exact premultiplied rational color,
 * `straight` its non-premultiplied form.
 */
export interface ColoredFragment {
  readonly rect: Rect;
  readonly color: PremulColor;
  readonly straight: StraightColor;
}

/**
 * Render a scene into non-overlapping colored fragments.
 *
 * The plane is partitioned along every rectangle edge, so each elementary
 * cell has a constant layer stack; the exact rational color of a cell is the
 * bottom-to-top "over" composition of the layers covering it, with group
 * opacities applied to their isolated buffers. Fully transparent cells are
 * dropped, and while compositing, layers below a fully opaque accumulated
 * layer are pruned (they cannot influence the result). Adjacent cells with
 * identical color are merged into maximal rectangles; the output is sorted
 * canonically (y0, x0, y1, x1).
 */
export function renderScene(sc: Scene): ColoredFragment[] {
  const leaves: Rect[] = [];
  collectRects(sc.root, leaves);
  if (leaves.length === 0) return [];

  const xs = uniqueSorted(leaves.flatMap((r) => [r.x0, r.x1]));
  const ys = uniqueSorted(leaves.flatMap((r) => [r.y0, r.y1]));

  // One color per elementary cell; null = fully transparent (pruned).
  const grid: (PremulColor | null)[][] = [];
  for (let yi = 0; yi + 1 < ys.length; yi++) {
    const row: (PremulColor | null)[] = [];
    for (let xi = 0; xi + 1 < xs.length; xi++) {
      const cell = rect(xs[xi]!, ys[yi]!, xs[xi + 1]!, ys[yi + 1]!);
      const c = evalNode(sc.root, cell);
      row.push(c.a.isZero ? null : c);
    }
    grid.push(row);
  }

  return mergeCells(xs, ys, grid);
}

/** Evaluate the exact composited color of the scene over an elementary cell. */
function evalNode(node: SceneNode, cell: Rect): PremulColor {
  if (node.kind === "rect") {
    const r = node.rect;
    const covered =
      r.x0 <= cell.x0 && cell.x1 <= r.x1 && r.y0 <= cell.y0 && cell.y1 <= r.y1;
    return covered ? toPremul(node.color) : TRANSPARENT;
  }
  if (node.opacity.isZero) return TRANSPARENT;
  // Fold children top-to-bottom; prune everything below full opacity.
  let acc: PremulColor = TRANSPARENT;
  for (let i = node.children.length - 1; i >= 0; i--) {
    acc = over(acc, evalNode(node.children[i]!, cell));
    if (acc.a.isOne) break; // fully occluded: lower layers cannot contribute
  }
  return scale(acc, node.opacity);
}

function collectRects(node: SceneNode, out: Rect[]): void {
  if (node.kind === "rect") {
    if (node.rect.x1 > node.rect.x0 && node.rect.y1 > node.rect.y0) {
      out.push(node.rect);
    }
    return;
  }
  for (const c of node.children) collectRects(c, out);
}

function uniqueSorted(values: number[]): number[] {
  return [...new Set(values)].sort((a, b) => a - b);
}

interface Run {
  x0: number;
  x1: number;
  color: PremulColor;
}

/**
 * Merge the cell grid into maximal rectangles: first horizontal runs per row
 * band, then vertical stacking of identical runs. Deterministic, hence
 * canonical for a given scene.
 */
function mergeCells(
  xs: number[],
  ys: number[],
  grid: (PremulColor | null)[][],
): ColoredFragment[] {
  const fragments: { r: Rect; color: PremulColor }[] = [];
  // Open fragments keyed by "x0,x1,colorKey", extending downwards.
  let open = new Map<string, { r: Rect; color: PremulColor }>();

  for (let yi = 0; yi < grid.length; yi++) {
    const y0 = ys[yi]!;
    const y1 = ys[yi + 1]!;
    const row = grid[yi]!;
    const runs: Run[] = [];
    let xi = 0;
    while (xi < row.length) {
      const c = row[xi]!;
      if (c === null) { xi++; continue; }
      const key = premulKey(c);
      let xj = xi + 1;
      while (xj < row.length && row[xj] !== null && premulKey(row[xj]!) === key) xj++;
      runs.push({ x0: xs[xi]!, x1: xs[xj]!, color: c });
      xi = xj;
    }

    const next = new Map<string, { r: Rect; color: PremulColor }>();
    for (const run of runs) {
      const key = `${run.x0},${run.x1},${premulKey(run.color)}`;
      const existing = open.get(key);
      if (existing && existing.r.y1 === y0) {
        next.set(key, {
          r: rect(existing.r.x0, existing.r.y0, existing.r.x1, y1),
          color: existing.color,
        });
        open.delete(key);
      } else {
        next.set(key, { r: rect(run.x0, y0, run.x1, y1), color: run.color });
      }
    }
    for (const f of open.values()) fragments.push(f);
    open = next;
  }
  for (const f of open.values()) fragments.push(f);

  fragments.sort((a, b) => compareRects(a.r, b.r));
  return fragments.map((f) => ({
    rect: f.r,
    color: f.color,
    straight: toStraight(f.color),
  }));
}
