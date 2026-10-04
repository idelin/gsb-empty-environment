// Range rule normalization.
//
// Rules map integer half-open boxes [lo, hi) to labels. Where boxes overlap,
// the highest priority rule wins. normalize() emits a canonical, non-
// overlapping partition: adjacent same-label regions are merged to a
// fixpoint, so the result does not depend on the input rule order.

export type Label = string;

export interface Box {
  lo: number[];
  hi: number[];
}

export interface Rule {
  box: Box;
  label: Label;
  priority: number;
}

export interface Region {
  box: Box;
  label: Label;
}

export interface VerifyReport {
  disjoint: boolean;
  volume: number;
  unionVolume: number;
  volumeMatches: boolean;
  labelsMatch: boolean;
  ok: boolean;
}

interface MergeResult {
  out: Region[];
  changed: boolean;
}

export const MAX_DIMS = 3;

export function boxVolume(box: Box): number {
  let vol = 1;
  for (let d = 0; d < box.lo.length; d++) {
    vol *= box.hi[d] - box.lo[d];
  }
  return vol;
}

export function containsPoint(box: Box, point: number[]): boolean {
  for (let d = 0; d < box.lo.length; d++) {
    if (point[d] < box.lo[d] || point[d] >= box.hi[d]) return false;
  }
  return true;
}

function boxesOverlap(a: Box, b: Box): boolean {
  for (let d = 0; d < a.lo.length; d++) {
    if (a.hi[d] <= b.lo[d] || b.hi[d] <= a.lo[d]) return false;
  }
  return true;
}

export function validateRules(rules: Rule[]): number {
  if (rules.length === 0) throw new Error("at least one rule is required");
  const dims = rules[0].box.lo.length;
  if (dims < 1 || dims > MAX_DIMS) {
    throw new Error("dimension must be between 1 and " + MAX_DIMS + ", got " + dims);
  }
  for (const r of rules) {
    if (r.box.lo.length !== dims || r.box.hi.length !== dims) {
      throw new Error("all rules must share the same dimension");
    }
    for (let d = 0; d < dims; d++) {
      const lo = r.box.lo[d];
      const hi = r.box.hi[d];
      if (!Number.isInteger(lo) || !Number.isInteger(hi)) {
        throw new Error("box coordinates must be integers");
      }
      if (!(lo < hi)) {
        throw new Error("box lo must be less than hi on every axis");
      }
    }
  }
  return dims;
}

function winningLabel(rules: Rule[], point: number[]): Label | undefined {
  let found = false;
  let bestPri = 0;
  let bestLabel = "";
  for (const r of rules) {
    if (!containsPoint(r.box, point)) continue;
    if (!found || r.priority > bestPri) {
      found = true;
      bestPri = r.priority;
      bestLabel = r.label;
    } else if (r.priority === bestPri && r.label !== bestLabel) {
      throw new Error(
        "conflicting rules at priority " + r.priority +
        ": labels '" + bestLabel + "' and '" + r.label + "' overlap"
      );
    }
  }
  return found ? bestLabel : undefined;
}

function buildAxes(rules: Rule[], dims: number): number[][] {
  const axes: number[][] = [];
  for (let d = 0; d < dims; d++) {
    const coords: Set<number> = new Set();
    for (const r of rules) {
      coords.add(r.box.lo[d]);
      coords.add(r.box.hi[d]);
    }
    const sorted = [...coords].sort((a, b) => a - b);
    axes.push(sorted);
  }
  return axes;
}

function cellsOf(axes: number[][]): Box[] {
  const dims = axes.length;
  const cells: Box[] = [];
  const idx: number[] = new Array(dims).fill(0);
  for (;;) {
    const lo: number[] = [];
    const hi: number[] = [];
    for (let d = 0; d < dims; d++) {
      lo.push(axes[d][idx[d]]);
      hi.push(axes[d][idx[d] + 1]);
    }
    cells.push({ lo, hi });
    let d = dims - 1;
    while (d >= 0) {
      idx[d]++;
      if (idx[d] < axes[d].length - 1) break;
      idx[d] = 0;
      d--;
    }
    if (d < 0) break;
  }
  return cells;
}

function sameExceptDim(a: Box, b: Box, dim: number): boolean {
  for (let d = 0; d < a.lo.length; d++) {
    if (d === dim) continue;
    if (a.lo[d] !== b.lo[d] || a.hi[d] !== b.hi[d]) return false;
  }
  return true;
}

function compareForDim(a: Region, b: Region, dim: number): number {
  if (a.label < b.label) return -1;
  if (a.label > b.label) return 1;
  const dims = a.box.lo.length;
  for (let d = 0; d < dims; d++) {
    if (d === dim) continue;
    if (a.box.lo[d] !== b.box.lo[d]) return a.box.lo[d] - b.box.lo[d];
    if (a.box.hi[d] !== b.box.hi[d]) return a.box.hi[d] - b.box.hi[d];
  }
  return a.box.lo[dim] - b.box.lo[dim];
}

function mergeAlong(regions: Region[], dim: number): MergeResult {
  const sorted = regions.slice().sort((a, b) => compareForDim(a, b, dim));
  const out: Region[] = [];
  let changed = false;
  for (const r of sorted) {
    const last = out.length > 0 ? out[out.length - 1] : undefined;
    if (
      last !== undefined &&
      last.label === r.label &&
      last.box.hi[dim] === r.box.lo[dim] &&
      sameExceptDim(last.box, r.box, dim)
    ) {
      last.box.hi[dim] = r.box.hi[dim];
      changed = true;
    } else {
      out.push({ box: { lo: r.box.lo.slice(), hi: r.box.hi.slice() }, label: r.label });
    }
  }
  return { out, changed };
}

function mergeAll(regions: Region[], dims: number): Region[] {
  let current = regions;
  let changed = true;
  while (changed) {
    changed = false;
    for (let d = 0; d < dims; d++) {
      const res = mergeAlong(current, d);
      current = res.out;
      changed = changed || res.changed;
    }
  }
  return current;
}

function compareRegions(a: Region, b: Region): number {
  const dims = a.box.lo.length;
  for (let d = 0; d < dims; d++) {
    if (a.box.lo[d] !== b.box.lo[d]) return a.box.lo[d] - b.box.lo[d];
  }
  for (let d = 0; d < dims; d++) {
    if (a.box.hi[d] !== b.box.hi[d]) return a.box.hi[d] - b.box.hi[d];
  }
  if (a.label < b.label) return -1;
  if (a.label > b.label) return 1;
  return 0;
}

export function normalize(rules: Rule[]): Region[] {
  const dims = validateRules(rules);
  const axes = buildAxes(rules, dims);
  const regions: Region[] = [];
  for (const cell of cellsOf(axes)) {
    const label = winningLabel(rules, cell.lo);
    if (label !== undefined) {
      regions.push({ box: cell, label });
    }
  }
  const merged = mergeAll(regions, dims);
  merged.sort(compareRegions);
  return merged;
}

export function queryPoint(regions: Region[], point: number[]): Label | undefined {
  for (const r of regions) {
    if (containsPoint(r.box, point)) return r.label;
  }
  return undefined;
}

export function queryRules(rules: Rule[], point: number[]): Label | undefined {
  validateRules(rules);
  return winningLabel(rules, point);
}

export function partitionVolume(regions: Region[]): number {
  let total = 0;
  for (const r of regions) total += boxVolume(r.box);
  return total;
}

export function unionVolume(rules: Rule[]): number {
  const dims = validateRules(rules);
  const axes = buildAxes(rules, dims);
  let total = 0;
  for (const cell of cellsOf(axes)) {
    for (const r of rules) {
      if (containsPoint(r.box, cell.lo)) {
        total += boxVolume(cell);
        break;
      }
    }
  }
  return total;
}

function pairwiseDisjoint(regions: Region[]): boolean {
  for (let i = 0; i < regions.length; i++) {
    for (let j = i + 1; j < regions.length; j++) {
      if (boxesOverlap(regions[i].box, regions[j].box)) return false;
    }
  }
  return true;
}

export function verifyPartition(rules: Rule[], regions: Region[]): VerifyReport {
  const dims = validateRules(rules);
  const axes = buildAxes(rules, dims);
  const disjoint = pairwiseDisjoint(regions);
  const volume = partitionVolume(regions);
  const expected = unionVolume(rules);
  const volumeMatches = volume === expected;
  let labelsMatch = true;
  for (const cell of cellsOf(axes)) {
    if (queryPoint(regions, cell.lo) !== winningLabel(rules, cell.lo)) {
      labelsMatch = false;
      break;
    }
  }
  const ok = disjoint && volumeMatches && labelsMatch;
  return { disjoint, volume, unionVolume: expected, volumeMatches, labelsMatch, ok };
}
