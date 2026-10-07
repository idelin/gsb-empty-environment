import { blockHeight } from "./builders.ts";
import { paginate } from "./paginate.ts";
import type { PaginationResult, PaginateOptions, TableSpec } from "./types.ts";

export interface EnumerationReport {
  /** Minimum feasible page count over all contiguous partitions. */
  minPages: number;
  /** Page count produced by the greedy paginator. */
  greedyPages: number;
  /** True when the greedy result (incl. last-page footnote reflow) is optimal. */
  greedyIsOptimal: boolean;
  /** Number of feasible partitions achieving minPages. */
  optimalPartitionCount: number;
  /** Total feasible partitions examined. */
  feasiblePartitionCount: number;
}

const MAX_ENUMERABLE_BLOCKS = 24;

/**
 * Brute-force every contiguous partition of the block sequence (cut points
 * between blocks; blocks stay atomic and in order). A partition is feasible
 * when every page's content fits `pageHeight - headerHeight`, and the last
 * page additionally leaves room for the footnote. Only practical for small
 * inputs — this exists to cross-check the greedy paginator.
 */
export function enumeratePartitions(
  spec: TableSpec,
  options: PaginateOptions,
): { minPages: number; feasiblePartitionCount: number; optimalPartitionCount: number } {
  const n = spec.blocks.length;
  if (n > MAX_ENUMERABLE_BLOCKS) {
    throw new Error(
      `Enumeration is exponential; refusing ${n} blocks (max ${MAX_ENUMERABLE_BLOCKS})`,
    );
  }
  const footnoteHeight = options.footnoteHeight ?? 0;
  const capacity = options.pageHeight - spec.headerHeight;
  const lastCapacity = capacity - footnoteHeight;
  // A virtual zero-height trailing block models the footnote-only last page:
  // the real last block may be too tall to share a page with the footnote,
  // in which case the optimum puts the footnote alone on a final page.
  const heights = [...spec.blocks.map(blockHeight), 0];
  const n1 = heights.length;

  // Prefix sums for O(1) page-height checks.
  const prefix = [0];
  for (const h of heights) prefix.push(prefix[prefix.length - 1] + h);
  const rangeHeight = (from: number, to: number) => prefix[to] - prefix[from]; // [from, to)

  let minPages = Number.POSITIVE_INFINITY;
  let feasiblePartitionCount = 0;
  let optimalPartitionCount = 0;

  // cuts[i] === true means a page boundary before block i (1 <= i < n1).
  const cuts: boolean[] = new Array(n1).fill(false);
  const visit = (i: number): void => {
    if (i === n1) {
      // Evaluate this partition.
      let pages = 1;
      let start = 0;
      let feasible = true;
      for (let k = 1; k < n1; k++) {
        if (cuts[k]) {
          if (rangeHeight(start, k) > capacity) {
            feasible = false;
            break;
          }
          pages += 1;
          start = k;
        }
      }
      if (feasible && rangeHeight(start, n1) > lastCapacity) feasible = false;
      if (!feasible) return;
      feasiblePartitionCount += 1;
      if (pages < minPages) {
        minPages = pages;
        optimalPartitionCount = 1;
      } else if (pages === minPages) {
        optimalPartitionCount += 1;
      }
      return;
    }
    cuts[i] = false;
    visit(i + 1);
    cuts[i] = true;
    visit(i + 1);
  };
  visit(1);

  return { minPages, feasiblePartitionCount, optimalPartitionCount };
}

/**
 * Cross-check the greedy paginator against exhaustive enumeration:
 * verifies structural validity of the result and that its page count —
 * including last-page footnote reflow — matches the enumerated optimum.
 */
export function verifyWithEnumeration(
  spec: TableSpec,
  options: PaginateOptions,
  result: PaginationResult = paginate(spec, options),
): EnumerationReport {
  const footnoteHeight = options.footnoteHeight ?? 0;
  const capacity = options.pageHeight - spec.headerHeight;

  // Structural checks on the greedy result.
  const flatIds = result.pages.flatMap((p) => p.blocks.map((b) => b.id));
  if (flatIds.length !== spec.blocks.length) {
    throw new Error(`Result places ${flatIds.length} blocks, expected ${spec.blocks.length}`);
  }
  spec.blocks.forEach((block, i) => {
    if (flatIds[i] !== block.id) {
      throw new Error(`Block order not preserved at position ${i}: ${flatIds[i]} !== ${block.id}`);
    }
  });
  result.pages.forEach((page, i) => {
    const isLast = i === result.pages.length - 1;
    if (page.contentHeight > capacity) {
      throw new Error(`Page ${i} content ${page.contentHeight} exceeds capacity ${capacity}`);
    }
    if (!isLast && page.footnoteHeight !== 0) {
      throw new Error(`Page ${i} carries a footnote but is not the last page`);
    }
    if (isLast && page.contentHeight + page.footnoteHeight > capacity) {
      throw new Error(
        `Last page content ${page.contentHeight} + footnote ${page.footnoteHeight} exceeds ${capacity}`,
      );
    }
    if (isLast && page.footnoteHeight !== footnoteHeight) {
      throw new Error(`Last page footnote ${page.footnoteHeight} !== expected ${footnoteHeight}`);
    }
  });

  const { minPages, feasiblePartitionCount, optimalPartitionCount } = enumeratePartitions(
    spec,
    options,
  );
  return {
    minPages,
    greedyPages: result.pageCount,
    greedyIsOptimal: result.pageCount === minPages,
    optimalPartitionCount,
    feasiblePartitionCount,
  };
}
