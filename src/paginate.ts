import { PaginationOverflowError } from "./errors.ts";
import { blockHeight } from "./builders.ts";
import type {
  BlockSpec,
  PageLayout,
  PaginationResult,
  PaginateOptions,
  PlacedBlock,
  TableSpec,
} from "./types.ts";

function assertFinitePositive(name: string, value: number): void {
  if (!Number.isFinite(value) || value < 0) {
    throw new Error(`${name} must be a non-negative finite number, got ${value}`);
  }
}

/**
 * Paginate a table by line height.
 *
 * - The header repeats on every page and consumes `headerHeight` of each page.
 * - Blocks (merged-cell row groups) are atomic and never split across pages.
 * - A block that can never fit under the header throws PaginationOverflowError
 *   naming that block, instead of looping forever emitting pages.
 * - `footnoteHeight` is reserved on the last page only; if the greedy tail no
 *   longer fits, whole blocks reflow forward onto a new last page.
 */
export function paginate(spec: TableSpec, options: PaginateOptions): PaginationResult {
  const { pageHeight } = options;
  const footnoteHeight = options.footnoteHeight ?? 0;
  assertFinitePositive("pageHeight", pageHeight);
  assertFinitePositive("headerHeight", spec.headerHeight);
  assertFinitePositive("footnoteHeight", footnoteHeight);

  const capacity = pageHeight - spec.headerHeight;
  if (capacity <= 0) {
    throw new Error(
      `pageHeight (${pageHeight}) must exceed headerHeight (${spec.headerHeight}); no room for any row`,
    );
  }
  if (footnoteHeight > capacity) {
    throw new Error(
      `footnoteHeight (${footnoteHeight}) exceeds per-page content capacity (${capacity})`,
    );
  }

  // Preflight: every block must fit under the header on a fresh page.
  // This is what guarantees the greedy loop below always makes progress.
  const heights = spec.blocks.map(blockHeight);
  heights.forEach((h, i) => {
    if (h > capacity) {
      const block = spec.blocks[i];
      throw new PaginationOverflowError({
        blockId: block.id,
        blockIndex: i,
        rowIds: block.rows.map((r) => r.id),
        required: h,
        available: capacity,
      });
    }
  });

  // Last-page footnote reservation, done constructively:
  // the last page takes the LONGEST suffix of blocks whose total height fits
  // capacity - footnoteHeight; the remaining prefix is packed greedily at full
  // capacity. Greedy page count is monotone non-increasing as the suffix
  // grows, so the longest feasible suffix minimizes the total page count.
  // An empty suffix yields a footnote-only final page (the trailing block
  // cannot share a page with the footnote).
  const lastCapacity = capacity - footnoteHeight;
  let suffixStart = heights.length;
  let suffixHeight = 0;
  while (suffixStart > 0 && suffixHeight + heights[suffixStart - 1] <= lastCapacity) {
    suffixStart -= 1;
    suffixHeight += heights[suffixStart];
  }
  const prefixBlocks = spec.blocks.slice(0, suffixStart);
  const suffixBlocks = spec.blocks.slice(suffixStart);

  // Greedy fill of the prefix at full capacity. The preflight guarantees
  // every block fits an empty page, so this always makes progress.
  const pageBlocks: BlockSpec[][] = [];
  let used = 0;
  prefixBlocks.forEach((block, i) => {
    const h = heights[i];
    if (used + h > capacity) {
      pageBlocks.push([]);
      used = 0;
    }
    if (pageBlocks.length === 0) pageBlocks.push([]);
    pageBlocks[pageBlocks.length - 1].push(block);
    used += h;
  });
  pageBlocks.push(suffixBlocks);

  const pages: PageLayout[] = pageBlocks.map((blocks, index) => {
    const isLast = index === pageBlocks.length - 1;
    const placed: PlacedBlock[] = [];
    let offsetY = spec.headerHeight;
    for (const block of blocks) {
      const rows = block.rows.map((r) => {
        const placedRow = { id: r.id, height: r.height, offsetY };
        offsetY += r.height;
        return placedRow;
      });
      placed.push({
        id: block.id,
        height: blockHeight(block),
        offsetY: rows.length > 0 ? rows[0].offsetY : offsetY,
        rows,
      });
    }
    const contentHeight = placed.reduce((s, b) => s + b.height, 0);
    const pageFootnote = isLast ? footnoteHeight : 0;
    return {
      index,
      headerHeight: spec.headerHeight,
      blocks: placed,
      contentHeight,
      footnoteHeight: pageFootnote,
      usedHeight: spec.headerHeight + contentHeight + pageFootnote,
    };
  });

  return {
    pages,
    pageCount: pages.length,
    pageHeight,
    contentCapacity: capacity,
    lastPageContentCapacity: lastCapacity,
  };
}
