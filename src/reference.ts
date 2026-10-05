import { Propagation, plateauInfo } from './propagate.ts';

export interface ReferenceResult {
  basin: Int32Array;
  basinDist: Int32Array;
  basinRoot: Map<number, number>;
  labels: Int32Array;
  labelDist: Int32Array;
}

/**
 * Full priority-flood reference segmentation.
 *
 * Phase 1 floods catchment basins out of every regional minimum; phase 2
 * floods labels out of the seeds, never crossing a basin boundary, so a
 * basin without a seed stays unassigned (label 0). Basin ids are assigned in
 * raster order of each minimum plateau's first cell, which makes the output
 * canonical and comparable across runs.
 *
 * This is the straightforward whole-image computation used to validate the
 * incremental engine: after any sequence of edits, the engine's label map
 * must equal a from-scratch reference run.
 */
export function referenceSegment(
  heights: Int32Array,
  w: number,
  h: number,
  seeds: Map<number, number>,
): ReferenceResult {
  const n = w * h;
  const basin = new Int32Array(n);
  const basinDist = new Int32Array(n);
  const basinRoot = new Map<number, number>();
  const seen = new Uint8Array(n);
  const basinSources: { idx: number; tag: number }[] = [];
  let nextId = 1;
  for (let i = 0; i < n; i++) {
    if (seen[i] === 1) continue;
    const plat = plateauInfo(heights, w, h, i);
    for (const c of plat.cells) seen[c] = 1;
    if (plat.minimum) {
      const id = nextId++;
      basinRoot.set(id, plat.cells[0]);
      for (const c of plat.cells) basinSources.push({ idx: c, tag: id });
    }
  }
  new Propagation({
    w,
    h,
    heights,
    tag: basin,
    dist: basinDist,
    region: null,
    sources: basinSources,
    allow: () => true,
  }).run();

  const labels = new Int32Array(n);
  const labelDist = new Int32Array(n);
  const labelSources: { idx: number; tag: number }[] = [];
  for (const [id, cell] of seeds) labelSources.push({ idx: cell, tag: id });
  new Propagation({
    w,
    h,
    heights,
    tag: labels,
    dist: labelDist,
    region: null,
    sources: labelSources,
    allow: (from, to) => basin[from] === basin[to],
  }).run();

  return { basin, basinDist, basinRoot, labels, labelDist };
}
