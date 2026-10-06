import { displacement, rectInCanvas, rectsOverlap } from "./geometry";
import {
  Canvas,
  ConflictPair,
  LabelSpec,
  Placement,
  SolveResult,
} from "./types";

const EPS = 1e-9;

export interface VisibleLabel {
  spec: LabelSpec;
  /** Indices of candidates that lie fully inside the canvas. */
  validCandidates: number[];
}

/**
 * Solve the label layout problem:
 *  - at most one candidate per visible label,
 *  - placed rects must not strictly overlap (touching edges is fine),
 *  - placed rects must lie fully inside the canvas,
 *  - hidden labels are never placed and never count toward the weight.
 *
 * Objective (lexicographic): maximize total placed weight, then minimize
 * total displacement (anchor to nearest rect point).
 *
 * Exact branch-and-bound over at most 12 labels x 4 candidates.
 */
export function solveLayout(labels: LabelSpec[], canvas: Canvas): SolveResult {
  const visible: VisibleLabel[] = [];
  const hiddenIds: string[] = [];

  for (const spec of labels) {
    if (spec.hidden) {
      hiddenIds.push(spec.id);
      continue;
    }
    const validCandidates: number[] = [];
    spec.candidates.forEach((cand, index) => {
      if (rectInCanvas(cand.rect, canvas)) validCandidates.push(index);
    });
    visible.push({ spec, validCandidates });
  }

  // Suffix sum of positive weights: upper bound for branch-and-bound.
  const remainingWeight = new Array<number>(visible.length + 1).fill(0);
  for (let i = visible.length - 1; i >= 0; i--) {
    remainingWeight[i] =
      remainingWeight[i + 1] + Math.max(0, visible[i].spec.weight);
  }

  const chosen: Placement[] = [];
  let best: Placement[] = [];
  let bestWeight = -Infinity;
  let bestDisplacement = Infinity;

  const consider = (weight: number, disp: number) => {
    if (
      weight > bestWeight + EPS ||
      (Math.abs(weight - bestWeight) <= EPS && disp < bestDisplacement - EPS)
    ) {
      bestWeight = weight;
      bestDisplacement = disp;
      best = chosen.slice();
    }
  };

  const search = (index: number, weight: number, disp: number): void => {
    if (weight + remainingWeight[index] < bestWeight - EPS) return;
    if (index === visible.length) {
      consider(weight, disp);
      return;
    }
    const label = visible[index];
    // Branch 1: try each valid, non-overlapping candidate.
    for (const candIndex of label.validCandidates) {
      const rect = label.spec.candidates[candIndex].rect;
      let blocked = false;
      for (const placed of chosen) {
        if (rectsOverlap(placed.rect, rect)) {
          blocked = true;
          break;
        }
      }
      if (blocked) continue;
      chosen.push({
        labelId: label.spec.id,
        candidateIndex: candIndex,
        rect,
        weight: label.spec.weight,
        displacement: displacement(label.spec.anchor, rect),
      });
      search(index + 1, weight + label.spec.weight, disp + chosen[chosen.length - 1].displacement);
      chosen.pop();
    }
    // Branch 2: leave the label unplaced.
    search(index + 1, weight, disp);
  };

  search(0, 0, 0);

  const placedIds = new Set(best.map((p) => p.labelId));
  return {
    placements: best,
    totalWeight: bestWeight,
    totalDisplacement: bestDisplacement,
    conflicts: computeConflicts(visible),
    unplaced: visible
      .map((v) => v.spec.id)
      .filter((id) => !placedIds.has(id)),
    hidden: hiddenIds,
  };
}

/**
 * Conflict pairs: pairs of visible labels that have at least one pair of
 * in-canvas candidates whose rects strictly overlap. These are the labels
 * that compete for space and explain why some cannot be placed together.
 */
export function computeConflicts(visible: VisibleLabel[]): ConflictPair[] {
  const conflicts: ConflictPair[] = [];
  for (let i = 0; i < visible.length; i++) {
    for (let j = i + 1; j < visible.length; j++) {
      let overlappingCandidates = 0;
      for (const ci of visible[i].validCandidates) {
        for (const cj of visible[j].validCandidates) {
          if (
            rectsOverlap(
              visible[i].spec.candidates[ci].rect,
              visible[j].spec.candidates[cj].rect,
            )
          ) {
            overlappingCandidates++;
          }
        }
      }
      if (overlappingCandidates > 0) {
        const [a, b] = [visible[i].spec.id, visible[j].spec.id].sort();
        conflicts.push({ labelA: a, labelB: b, overlappingCandidates });
      }
    }
  }
  return conflicts;
}
