/**
 * Track size distribution engine.
 *
 * Supports three track kinds:
 *   - fixed:        exact size, never grows or shrinks
 *   - content:      sized to the largest minimum width of items wholly
 *                   contained in the track, may absorb span deficits
 *   - proportional: receives a weight-proportional share of the free space
 *                   left after every track's base size and the gaps
 *
 * Spanning items (columnSpan >= 2) contribute a constraint that the SUM of
 * the spanned tracks plus the gaps between them must be at least the item's
 * minimum width. Resolving those constraints is iterative and monotone:
 * base sizes only ever grow and are bounded above, so the loop is
 * guaranteed to converge to a fixpoint.
 *
 * If the container cannot hold the sum of base sizes plus gaps, the engine
 * reports an overflow diagnostic instead of producing negative widths.
 * Proportional distribution divides by the total weight only when it is
 * positive; zero-weight configurations never divide by zero.
 */

import type { GridItem } from "./tree.ts";

export type TrackDefinition =
  | { readonly kind: "fixed"; readonly size: number }
  | { readonly kind: "content" }
  | { readonly kind: "proportional"; readonly weight: number };

export type Diagnostic =
  | {
      readonly kind: "overflow";
      /** How many device units the minimum content exceeds the container. */
      readonly amount: number;
      readonly message: string;
    }
  | {
      readonly kind: "undistributed-free-space";
      readonly amount: number;
      readonly message: string;
    }
  | {
      readonly kind: "span-out-of-range";
      readonly itemId: string;
      readonly message: string;
    };

export interface TrackSizingOptions {
  readonly tracks: readonly TrackDefinition[];
  readonly items: readonly GridItem[];
  readonly containerWidth: number;
  /** Gap between adjacent tracks. Defaults to 0. */
  readonly gap?: number;
  /** Safety bound for the span-resolution loop. Defaults to 100. */
  readonly maxIterations?: number;
}

export interface TrackSizingResult {
  /** Final size of every track. Never negative. */
  readonly sizes: readonly number[];
  /** Base sizes after span resolution, before free-space distribution. */
  readonly baseSizes: readonly number[];
  /** X offset of every track, gap-aware. */
  readonly positions: readonly number[];
  /** Sum of sizes plus gaps: the width the content actually occupies. */
  readonly usedWidth: number;
  /** Iterations the span-resolution loop needed. */
  readonly iterations: number;
  /** Whether the loop reached a fixpoint within `maxIterations`. */
  readonly converged: boolean;
  readonly diagnostics: readonly Diagnostic[];
}

const EPSILON = 1e-9;

function totalGap(trackCount: number, gap: number): number {
  return trackCount > 1 ? gap * (trackCount - 1) : 0;
}

/**
 * Initialize base sizes: fixed tracks take their exact size, content tracks
 * take the largest minimum width among items that live entirely inside
 * them, proportional tracks start at zero and grow later from free space.
 */
function initializeBaseSizes(
  tracks: readonly TrackDefinition[],
  items: readonly GridItem[],
): number[] {
  const base = tracks.map((track) =>
    track.kind === "fixed" ? track.size : 0,
  );
  for (const item of items) {
    if (item.columnSpan !== 1) continue;
    const index = item.columnStart;
    if (index < 0 || index >= tracks.length) continue;
    if (tracks[index].kind !== "content") continue;
    if (item.minWidth > base[index]) base[index] = item.minWidth;
  }
  return base;
}

/**
 * Iteratively grow base sizes until every spanning item's minimum width is
 * covered by the spanned tracks plus the gaps between them.
 *
 * Deficits are shared equally by the growable spanned tracks (content and
 * proportional). If a span covers only fixed tracks, the fixed tracks grow
 * too: a minimum-width constraint is a hard constraint and violating it
 * would silently corrupt the layout.
 *
 * Each pass only increases base sizes, and every increase is bounded by a
 * finite item constraint, so the sequence converges. The loop exits as soon
 * as a full pass makes no change larger than EPSILON.
 */
function resolveSpanConstraints(
  tracks: readonly TrackDefinition[],
  items: readonly GridItem[],
  base: number[],
  gap: number,
  maxIterations: number,
  diagnostics: Diagnostic[],
): { iterations: number; converged: boolean } {
  const spanning = items
    .filter((item) => item.columnSpan > 1)
    .sort((a, b) => a.columnSpan - b.columnSpan);

  let iterations = 0;
  let converged = false;

  for (let pass = 0; pass < maxIterations; pass++) {
    iterations = pass + 1;
    let changed = false;

    for (const item of spanning) {
      const start = item.columnStart;
      const end = start + item.columnSpan;
      if (start < 0 || end > tracks.length) {
        if (
          !diagnostics.some(
            (d) => d.kind === "span-out-of-range" && d.itemId === item.id,
          )
        ) {
          diagnostics.push({
            kind: "span-out-of-range",
            itemId: item.id,
            message: `Item "${item.id}" spans columns ${start}..${end - 1} but only ${tracks.length} tracks exist; constraint ignored.`,
          });
        }
        continue;
      }

      let spanned = 0;
      for (let i = start; i < end; i++) spanned += base[i];
      spanned += gap * (item.columnSpan - 1);

      const deficit = item.minWidth - spanned;
      if (deficit <= EPSILON) continue;

      const growable: number[] = [];
      for (let i = start; i < end; i++) {
        if (tracks[i].kind !== "fixed") growable.push(i);
      }
      const targets = growable.length > 0 ? growable : range(start, end);
      const share = deficit / targets.length;
      for (const i of targets) base[i] += share;
      changed = true;
    }

    if (!changed) {
      converged = true;
      break;
    }
  }

  return { iterations, converged };
}

function range(start: number, end: number): number[] {
  const out: number[] = [];
  for (let i = start; i < end; i++) out.push(i);
  return out;
}

export function sizeTracks(options: TrackSizingOptions): TrackSizingResult {
  const { tracks, items, containerWidth } = options;
  const gap = options.gap ?? 0;
  const maxIterations = options.maxIterations ?? 100;
  const diagnostics: Diagnostic[] = [];

  const base = initializeBaseSizes(tracks, items);
  const { iterations, converged } = resolveSpanConstraints(
    tracks,
    items,
    base,
    gap,
    maxIterations,
    diagnostics,
  );

  const gapsTotal = totalGap(tracks.length, gap);
  const baseTotal = base.reduce((sum, size) => sum + size, 0);
  const minimumWidth = baseTotal + gapsTotal;

  const sizes = [...base];

  if (minimumWidth > containerWidth + EPSILON) {
    // Container cannot hold the minimum content. Report the overflow and
    // keep every track at its (non-negative) base size; never go negative.
    const amount = minimumWidth - containerWidth;
    diagnostics.push({
      kind: "overflow",
      amount,
      message: `Content needs ${round(minimumWidth)} but the container is ${round(containerWidth)}; overflow of ${round(amount)}.`,
    });
  } else {
    const freeSpace = containerWidth - minimumWidth;
    const totalWeight = tracks.reduce(
      (sum, track) =>
        track.kind === "proportional" ? sum + track.weight : sum,
      0,
    );
    if (totalWeight > 0 && freeSpace > EPSILON) {
      for (let i = 0; i < tracks.length; i++) {
        const track = tracks[i];
        if (track.kind === "proportional") {
          sizes[i] += (freeSpace * track.weight) / totalWeight;
        }
      }
    } else if (freeSpace > EPSILON) {
      // No proportional track (or all weights are zero): nothing may absorb
      // the free space. Report it instead of dividing by zero.
      diagnostics.push({
        kind: "undistributed-free-space",
        amount: freeSpace,
        message: `${round(freeSpace)} of free space could not be distributed: no proportional track with a positive weight.`,
      });
    }
  }

  const positions: number[] = [];
  let cursor = 0;
  for (let i = 0; i < sizes.length; i++) {
    positions.push(cursor);
    cursor += sizes[i] + gap;
  }

  const usedWidth = sizes.reduce((sum, size) => sum + size, 0) + gapsTotal;

  return {
    sizes,
    baseSizes: base,
    positions,
    usedWidth,
    iterations,
    converged,
    diagnostics,
  };
}

function round(value: number): number {
  return Math.round(value * 1000) / 1000;
}
