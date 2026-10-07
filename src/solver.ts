import {
  Rect,
  Vec,
  distancePointToRect,
  rectInsideCanvas,
  rectsOverlap,
} from "./geometry";

export const EPS = 1e-9;

export interface Label {
  id: string;
  anchor: Vec;
  weight: number;
  /** Up to 4 candidate placements for this label. */
  candidates: Rect[];
}

export interface LayoutInput {
  canvas: Rect;
  labels: Label[];
}

export interface PlacedLabel {
  id: string;
  candidateIndex: number;
  rect: Rect;
  weight: number;
  displacement: number;
}

export interface LayoutResult {
  /** Labels that were successfully placed. */
  placed: PlacedLabel[];
  /** Ids of labels that could not be placed and stay hidden. */
  unplaced: string[];
  totalWeight: number;
  totalDisplacement: number;
}

/** One overlapping candidate pair between two distinct labels. */
export interface CandidateConflict {
  a: string;
  b: string;
  candidateA: number;
  candidateB: number;
}

/**
 * Classic 4-position model: top-right, top-left, bottom-right, bottom-left
 * of the anchor, each `gap` units away from it.
 */
export function fourCandidates(anchor: Vec, w: number, h: number, gap = 0): Rect[] {
  return [
    { x: anchor.x + gap, y: anchor.y - h - gap, w, h },
    { x: anchor.x - w - gap, y: anchor.y - h - gap, w, h },
    { x: anchor.x + gap, y: anchor.y + gap, w, h },
    { x: anchor.x - w - gap, y: anchor.y + gap, w, h },
  ];
}

export function candidateDisplacement(label: Label, candidateIndex: number): number {
  return distancePointToRect(label.anchor, label.candidates[candidateIndex]);
}

/**
 * All candidate-level conflict pairs: label A's candidate i overlaps
 * label B's candidate j. Purely geometric (canvas feasibility ignored).
 */
export function computeConflictPairs(labels: Label[]): CandidateConflict[] {
  const conflicts: CandidateConflict[] = [];
  for (let i = 0; i < labels.length; i++) {
    for (let j = i + 1; j < labels.length; j++) {
      const a = labels[i];
      const b = labels[j];
      for (let ci = 0; ci < a.candidates.length; ci++) {
        for (let cj = 0; cj < b.candidates.length; cj++) {
          if (rectsOverlap(a.candidates[ci], b.candidates[cj])) {
            conflicts.push({ a: a.id, b: b.id, candidateA: ci, candidateB: cj });
          }
        }
      }
    }
  }
  return conflicts;
}

/**
 * Exact branch-and-bound solver.
 *
 * Objective (lexicographic):
 *   1. maximize total weight of placed labels;
 *   2. among those, minimize total displacement (anchor to nearest rect edge).
 *
 * A label is either placed on one of its candidates (inside the canvas and
 * non-overlapping other placed labels) or dropped (hidden). Dropped labels
 * contribute nothing and are reported in `unplaced`.
 */
export function solveLabelLayout(input: LayoutInput): LayoutResult {
  const { canvas, labels } = input;

  // Precompute per-label feasible candidates (inside canvas) with displacement.
  interface Prepared {
    index: number;
    weight: number;
    options: { candidateIndex: number; rect: Rect; displacement: number }[];
  }
  const prepared: Prepared[] = labels.map((label, index) => {
    const options: Prepared["options"] = [];
    for (let ci = 0; ci < label.candidates.length; ci++) {
      const rect = label.candidates[ci];
      if (rectInsideCanvas(rect, canvas)) {
        options.push({
          candidateIndex: ci,
          rect,
          displacement: candidateDisplacement(label, ci),
        });
      }
    }
    // Try cheaper candidates first so the displacement bound tightens early.
    options.sort((p, q) => p.displacement - q.displacement);
    return { index, weight: label.weight, options };
  });

  // Heavier labels first: the weight upper bound prunes harder.
  prepared.sort((a, b) => b.weight - a.weight || a.index - b.index);

  // Suffix sums of weights for the optimistic bound.
  const suffixWeight = new Array<number>(prepared.length + 1).fill(0);
  for (let i = prepared.length - 1; i >= 0; i--) {
    suffixWeight[i] = suffixWeight[i + 1] + Math.max(0, prepared[i].weight);
  }

  const chosenRects: Rect[] = [];
  const chosenAssignment = new Array<number>(prepared.length).fill(-1);
  let bestAssignment = new Array<number>(prepared.length).fill(-1);
  let bestWeight = 0;
  let bestDisplacement = Number.POSITIVE_INFINITY;

  function better(weight: number, displacement: number): boolean {
    return (
      weight > bestWeight + EPS ||
      (Math.abs(weight - bestWeight) <= EPS && displacement < bestDisplacement - EPS)
    );
  }

  function dfs(pos: number, weight: number, displacement: number): void {
    // Prune: even placing everything remaining cannot beat/tie the incumbent.
    if (weight + suffixWeight[pos] < bestWeight - EPS) return;
    if (
      Math.abs(weight + suffixWeight[pos] - bestWeight) <= EPS &&
      displacement >= bestDisplacement - EPS
    ) {
      return;
    }
    if (pos === prepared.length) {
      if (better(weight, displacement)) {
        bestWeight = weight;
        bestDisplacement = displacement;
        bestAssignment = chosenAssignment.slice();
      }
      return;
    }

    const item = prepared[pos];
    for (const option of item.options) {
      let clear = true;
      for (const placed of chosenRects) {
        if (rectsOverlap(option.rect, placed)) {
          clear = false;
          break;
        }
      }
      if (!clear) continue;
      chosenRects.push(option.rect);
      chosenAssignment[pos] = option.candidateIndex;
      dfs(pos + 1, weight + item.weight, displacement + option.displacement);
      chosenAssignment[pos] = -1;
      chosenRects.pop();
    }
    // Drop (hide) this label.
    chosenAssignment[pos] = -1;
    dfs(pos + 1, weight, displacement);
    chosenAssignment[pos] = -1;
  }

  dfs(0, 0, 0);

  const placed: PlacedLabel[] = [];
  const unplaced: string[] = [];
  for (let pos = 0; pos < prepared.length; pos++) {
    const label = labels[prepared[pos].index];
    const candidateIndex = bestAssignment[pos];
    if (candidateIndex < 0) {
      unplaced.push(label.id);
    } else {
      const rect = label.candidates[candidateIndex];
      placed.push({
        id: label.id,
        candidateIndex,
        rect,
        weight: label.weight,
        displacement: candidateDisplacement(label, candidateIndex),
      });
    }
  }
  placed.sort((a, b) => a.id.localeCompare(b.id));
  unplaced.sort();

  return {
    placed,
    unplaced,
    totalWeight: placed.reduce((sum, p) => sum + p.weight, 0),
    totalDisplacement: placed.reduce((sum, p) => sum + p.displacement, 0),
  };
}
