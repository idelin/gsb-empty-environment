/**
 * Reference oracle: single-shot full-image priority flood. The incremental
 * tiled engine must always converge to exactly this result.
 */
import {
  buildFronts, computeMinima, createFloodCursor, createStateMap, floodStep,
  type Front, type SeedLike, type StateMap,
} from './core.ts';

export interface ReferenceResult {
  labels: Int32Array;
  fronts: Front[];
  state: StateMap;
}

export function segmentReference(
  width: number,
  height: number,
  heights: Int32Array,
  seeds: SeedLike[],
): ReferenceResult {
  const minima = computeMinima(width, height, heights);
  const { fronts, sources } = buildFronts(seeds, minima, width);
  const state = createStateMap(width * height);
  const cursor = createFloodCursor(fronts);
  for (const s of sources) {
    cursor.heap.push({ level: heights[s.pixel], dist: -1, front: s.front, pixel: s.pixel });
  }
  floodStep({ width, height, heights, fronts, region: null, state }, cursor, Infinity);
  const labels = new Int32Array(width * height);
  for (let p = 0; p < labels.length; p++) {
    labels[p] = state.front[p] === -1 ? 0 : fronts[state.front[p]].label;
  }
  return { labels, fronts, state };
}
