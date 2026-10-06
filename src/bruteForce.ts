import { displacement, rectInCanvas, rectsOverlap } from "./geometry";
import { Canvas, LabelSpec } from "./types";

export interface BruteForceOptimum {
  totalWeight: number;
  totalDisplacement: number;
}

const EPS = 1e-9;

/**
 * Independent exhaustive enumeration of every combination of candidate
 * choices (including "not placed") over all visible labels. Used to
 * cross-check the solver: it must find the same lexicographic optimum of
 * (max total weight, min total displacement).
 *
 * Cost is O(prod(candidates_i + 1)); intended for small instances.
 */
export function bruteForceOptimum(
  labels: LabelSpec[],
  canvas: Canvas,
): BruteForceOptimum {
  const visible = labels.filter((l) => !l.hidden);
  // choices[i] = candidate index for label i, or -1 for "not placed".
  const choices = new Array<number>(visible.length).fill(-1);
  let bestWeight = -Infinity;
  let bestDisplacement = Infinity;

  const evaluate = (): void => {
    let weight = 0;
    let disp = 0;
    for (let i = 0; i < visible.length; i++) {
      const ci = choices[i];
      if (ci < 0) continue;
      const rect = visible[i].candidates[ci].rect;
      if (!rectInCanvas(rect, canvas)) return;
      for (let j = 0; j < i; j++) {
        const cj = choices[j];
        if (cj < 0) continue;
        if (rectsOverlap(visible[j].candidates[cj].rect, rect)) return;
      }
      weight += visible[i].weight;
      disp += displacement(visible[i].anchor, rect);
    }
    if (
      weight > bestWeight + EPS ||
      (Math.abs(weight - bestWeight) <= EPS && disp < bestDisplacement - EPS)
    ) {
      bestWeight = weight;
      bestDisplacement = disp;
    }
  };

  const enumerate = (index: number): void => {
    if (index === visible.length) {
      evaluate();
      return;
    }
    choices[index] = -1;
    enumerate(index + 1);
    for (let c = 0; c < visible[index].candidates.length; c++) {
      choices[index] = c;
      enumerate(index + 1);
    }
    choices[index] = -1;
  };

  enumerate(0);
  if (bestWeight === -Infinity) {
    // No visible labels at all: the empty placement is the optimum.
    return { totalWeight: 0, totalDisplacement: 0 };
  }
  return { totalWeight: bestWeight, totalDisplacement: bestDisplacement };
}
