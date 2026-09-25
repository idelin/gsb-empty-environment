// Track sizing: resolves fixed / content / fraction tracks against a
// container width, honouring spanning-item minimum widths and gaps.
// The fraction distribution is iterative and provably converges: every
// iteration either finishes or freezes at least one of finitely many tracks.

import type { LayoutContainer, LayoutItem, TrackSize } from "./tree.ts";
import { minContentWidth, type FontMetrics } from "./glyphs.ts";

export interface OverflowDiagnostic {
  kind: "container" | "item";
  message: string;
  // How many px are missing (always > 0).
  deficit: number;
  itemId?: string;
}

export interface TrackSizingResult {
  // Final resolved size of each column in px. Never negative, never NaN.
  sizes: number[];
  diagnostics: OverflowDiagnostic[];
  // Number of iterations the fraction distributor needed (for convergence checks).
  fractionIterations: number;
}

interface ItemDemand {
  item: LayoutItem;
  minWidth: number;
}

function trackFloor(track: TrackSize): number {
  switch (track.kind) {
    case "fixed":
      return track.px;
    case "content":
      return 0;
    case "fraction":
      return track.min ?? 0;
  }
}

// Distribute `amount` equally across the given track indexes, raising base sizes.
function growTracks(base: number[], indexes: number[], amount: number): void {
  if (indexes.length === 0 || amount <= 0) return;
  const share = amount / indexes.length;
  for (const index of indexes) {
    base[index] += share;
  }
}

// Iteratively resolve fraction tracks into `leftover` px, respecting floors.
// Zero-weight tracks only ever receive their floor; a zero total weight is
// handled without dividing by zero.
export function resolveFractions(
  weights: number[],
  floors: number[],
  leftover: number,
): { sizes: number[]; iterations: number } {
  const count = weights.length;
  const sizes = new Array<number>(count).fill(0);
  const frozen = new Array<boolean>(count).fill(false);
  let remaining = Math.max(0, leftover);
  let iterations = 0;

  for (;;) {
    iterations++;
    let totalWeight = 0;
    for (let i = 0; i < count; i++) {
      if (!frozen[i]) totalWeight += weights[i];
    }
    if (totalWeight <= 0) {
      // No (unfrozen) weight left: zero-weight tracks get exactly their floor.
      for (let i = 0; i < count; i++) {
        if (!frozen[i]) sizes[i] = floors[i];
      }
      return { sizes, iterations };
    }

    let frozeAny = false;
    for (let i = 0; i < count; i++) {
      if (frozen[i]) continue;
      const share = (remaining * weights[i]) / totalWeight;
      if (share < floors[i]) {
        sizes[i] = floors[i];
        frozen[i] = true;
        remaining -= floors[i];
        frozeAny = true;
      }
    }
    if (!frozeAny) {
      for (let i = 0; i < count; i++) {
        if (!frozen[i]) sizes[i] = (remaining * weights[i]) / totalWeight;
      }
      return { sizes, iterations };
    }
    if (remaining < 0) remaining = 0;
  }
}

export function sizeTracks(container: LayoutContainer, font: FontMetrics): TrackSizingResult {
  const { columns, gap, items, width } = container;
  const count = columns.length;
  const diagnostics: OverflowDiagnostic[] = [];

  // 1. Base sizes: fixed tracks take their px, fractions take their floor,
  //    content tracks start at zero and are grown by items below.
  const base = columns.map(trackFloor);

  // 2. Item minimum contributions, smallest span first so that spanning
  //    items see already-resolved single-track sizes.
  const demands: ItemDemand[] = items
    .map((item) => ({ item, minWidth: minContentWidth(item.text, item.fontSize, font) }))
    .sort((a, b) => a.item.columnSpan - b.item.columnSpan);

  for (const { item, minWidth } of demands) {
    const start = item.column;
    const end = start + item.columnSpan;
    const spanGaps = gap * (item.columnSpan - 1);
    let current = spanGaps;
    for (let i = start; i < end; i++) current += base[i];
    const deficit = minWidth - current;
    if (deficit <= 0) continue;

    // Prefer growing content tracks; otherwise raise fraction floors;
    // fixed-only spans cannot grow and are reported as item overflow.
    const growable: number[] = [];
    for (let i = start; i < end; i++) {
      if (columns[i].kind === "content") growable.push(i);
    }
    if (growable.length === 0) {
      for (let i = start; i < end; i++) {
        if (columns[i].kind === "fraction") growable.push(i);
      }
    }
    if (growable.length === 0) {
      diagnostics.push({
        kind: "item",
        itemId: item.id,
        deficit,
        message:
          `item "${item.id}" needs ${round2(minWidth)}px but its ${item.columnSpan} fixed ` +
          `track(s) plus gaps only provide ${round2(current)}px`,
      });
      continue;
    }
    growTracks(base, growable, deficit);
  }

  // 3. Split the container between definite tracks and fraction tracks.
  const totalGaps = gap * (count - 1);
  const weights: number[] = [];
  const floors: number[] = [];
  const fractionIndex: number[] = [];
  let definiteTotal = totalGaps;
  for (let i = 0; i < count; i++) {
    if (columns[i].kind === "fraction") {
      fractionIndex.push(i);
      weights.push((columns[i] as { weight: number }).weight);
      floors.push(base[i]);
    } else {
      definiteTotal += base[i];
    }
  }

  const leftover = width - definiteTotal;
  const fractionResult = resolveFractions(weights, floors, leftover);
  const sizes = base.slice();
  let fractionTotal = 0;
  for (let k = 0; k < fractionIndex.length; k++) {
    sizes[fractionIndex[k]] = fractionResult.sizes[k];
    fractionTotal += fractionResult.sizes[k];
  }

  // 4. Container overflow: the minimum required width exceeds the container.
  //    Tracks are clamped to their floors (never negative) and the deficit
  //    is reported instead of producing negative widths.
  const used = definiteTotal + fractionTotal;
  if (used > width) {
    diagnostics.push({
      kind: "container",
      deficit: used - width,
      message:
        `container is ${round2(width)}px but tracks need at least ${round2(used)}px ` +
        `(overflow ${round2(used - width)}px)`,
    });
  }

  for (let i = 0; i < count; i++) {
    if (!Number.isFinite(sizes[i]) || sizes[i] < 0) {
      // Defensive: the algorithm above should never produce this.
      sizes[i] = 0;
    }
  }

  return { sizes, diagnostics, fractionIterations: fractionResult.iterations };
}

function round2(value: number): number {
  return Math.round(value * 100) / 100;
}
