import { rectInsideCanvas, rectsOverlap } from "./geometry";
import {
  EPS,
  LayoutInput,
  LayoutResult,
  candidateDisplacement,
} from "./solver";

export interface VerificationReport {
  ok: boolean;
  /** Optimum found by the independent exhaustive enumeration. */
  optimalWeight: number;
  optimalDisplacement: number;
  /** Number of complete assignments the enumeration evaluated. */
  assignmentsExplored: number;
  errors: string[];
}

/**
 * Independent cross-check of a solver result.
 *
 * Phase 1 (consistency, "hidden labels cannot masquerade as placed"):
 *   - `placed` and `unplaced` form an exact partition of the input labels;
 *   - every placed entry references a real candidate, inside the canvas,
 *     pairwise non-overlapping (touching edges allowed);
 *   - reported totals equal sums recomputed from the placed entries.
 *
 * Phase 2 (optimality): a full combinatorial enumeration over every
 * assignment label -> {candidate 0..k-1 | dropped}. Branches are cut only
 * by exact infeasibility (canvas exit / overlap), never by heuristics, so
 * every feasible combination is evaluated. The solver result must match the
 * enumerated optimum in both total weight and total displacement.
 */
export function verifyLayout(input: LayoutInput, result: LayoutResult): VerificationReport {
  const errors: string[] = [];
  const { canvas, labels } = input;

  // ---- Phase 1: structural consistency of the reported result ----
  const byId = new Map(labels.map((l) => [l.id, l]));
  const seen = new Set<string>();
  for (const p of result.placed) {
    const label = byId.get(p.id);
    if (!label) {
      errors.push(`placed id "${p.id}" is not an input label`);
      continue;
    }
    if (seen.has(p.id)) errors.push(`label "${p.id}" placed more than once`);
    seen.add(p.id);
    if (p.candidateIndex < 0 || p.candidateIndex >= label.candidates.length) {
      errors.push(`label "${p.id}" uses invalid candidate index ${p.candidateIndex}`);
      continue;
    }
    const rect = label.candidates[p.candidateIndex];
    if (!rectInsideCanvas(rect, canvas)) {
      errors.push(`label "${p.id}" placed outside the canvas`);
    }
    if (Math.abs(p.weight - label.weight) > EPS) {
      errors.push(`label "${p.id}" reports weight ${p.weight}, expected ${label.weight}`);
    }
    const expectedDisp = candidateDisplacement(label, p.candidateIndex);
    if (Math.abs(p.displacement - expectedDisp) > EPS) {
      errors.push(
        `label "${p.id}" reports displacement ${p.displacement}, expected ${expectedDisp}`,
      );
    }
  }
  for (let i = 0; i < result.placed.length; i++) {
    for (let j = i + 1; j < result.placed.length; j++) {
      const a = byId.get(result.placed[i].id);
      const b = byId.get(result.placed[j].id);
      if (!a || !b) continue;
      const ra = a.candidates[result.placed[i].candidateIndex];
      const rb = b.candidates[result.placed[j].candidateIndex];
      if (ra && rb && rectsOverlap(ra, rb)) {
        errors.push(
          `placed labels "${a.id}" and "${b.id}" overlap (candidates ` +
            `${result.placed[i].candidateIndex} vs ${result.placed[j].candidateIndex})`,
        );
      }
    }
  }
  for (const id of result.unplaced) {
    if (!byId.has(id)) errors.push(`unplaced id "${id}" is not an input label`);
    if (seen.has(id)) {
      errors.push(`hidden label "${id}" also claims a placement`);
    }
    seen.add(id);
  }
  for (const label of labels) {
    if (!seen.has(label.id)) {
      errors.push(`label "${label.id}" is neither placed nor reported hidden`);
    }
  }
  const weightSum = result.placed.reduce((s, p) => s + p.weight, 0);
  const dispSum = result.placed.reduce((s, p) => s + p.displacement, 0);
  if (Math.abs(weightSum - result.totalWeight) > EPS) {
    errors.push(`reported totalWeight ${result.totalWeight} != recomputed ${weightSum}`);
  }
  if (Math.abs(dispSum - result.totalDisplacement) > EPS) {
    errors.push(
      `reported totalDisplacement ${result.totalDisplacement} != recomputed ${dispSum}`,
    );
  }

  // ---- Phase 2: exhaustive enumeration of all combinations ----
  const feasible: { rectIndex: number; displacement: number }[][] = labels.map((label) => {
    const options: { rectIndex: number; displacement: number }[] = [];
    for (let ci = 0; ci < label.candidates.length; ci++) {
      if (rectInsideCanvas(label.candidates[ci], canvas)) {
        options.push({
          rectIndex: ci,
          displacement: candidateDisplacement(label, ci),
        });
      }
    }
    return options;
  });

  let optimalWeight = 0;
  let optimalDisplacement = Number.POSITIVE_INFINITY;
  let assignmentsExplored = 0;
  const stack: number[] = new Array(labels.length).fill(-1);

  function enumerate(pos: number, weight: number, displacement: number): void {
    if (pos === labels.length) {
      assignmentsExplored++;
      if (
        weight > optimalWeight + EPS ||
        (Math.abs(weight - optimalWeight) <= EPS &&
          displacement < optimalDisplacement - EPS)
      ) {
        optimalWeight = weight;
        optimalDisplacement = displacement;
      }
      return;
    }
    const label = labels[pos];
    for (const option of feasible[pos]) {
      let clear = true;
      for (let prev = 0; prev < pos; prev++) {
        const prevCandidate = stack[prev];
        if (prevCandidate < 0) continue;
        if (rectsOverlap(label.candidates[option.rectIndex], labels[prev].candidates[prevCandidate])) {
          clear = false;
          break;
        }
      }
      if (!clear) continue;
      stack[pos] = option.rectIndex;
      enumerate(pos + 1, weight + label.weight, displacement + option.displacement);
      stack[pos] = -1;
    }
    stack[pos] = -1; // dropped / hidden
    enumerate(pos + 1, weight, displacement);
    stack[pos] = -1;
  }
  enumerate(0, 0, 0);

  if (Math.abs(result.totalWeight - optimalWeight) > EPS) {
    errors.push(
      `totalWeight ${result.totalWeight} is not optimal; exhaustive optimum is ${optimalWeight}`,
    );
  }
  if (Math.abs(result.totalDisplacement - optimalDisplacement) > EPS) {
    errors.push(
      `totalDisplacement ${result.totalDisplacement} is not optimal; ` +
        `exhaustive optimum at weight ${optimalWeight} is ${optimalDisplacement}`,
    );
  }

  return {
    ok: errors.length === 0,
    optimalWeight,
    optimalDisplacement,
    assignmentsExplored,
    errors,
  };
}
