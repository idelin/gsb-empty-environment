import { PagePacker } from './maxrects.ts';
import { exactPack, lowerBoundPages } from './exact.ts';
import {
  footprintSize,
  itemMargin,
  itemRotatable,
  packingOrder,
  spacingPad,
  spriteOrigin,
  compareIds,
} from './geometry.ts';
import type {
  ItemSpec,
  LayoutInput,
  PackResult,
  Placement,
  UnpackedItem,
} from './types.ts';

export interface PackOptions {
  /** Run the exact branch-and-bound when the number of movable items is <= this. Default 10. */
  exactMaxItems?: number;
  /** Node budget for the exact search. */
  exactNodeLimit?: number;
}

interface FixedObstacle {
  page: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Footprint rect (clipped to the page) for an already-located sprite. */
export function footprintRectOf(
  item: ItemSpec,
  placement: { x: number; y: number; width: number; height: number },
  spacing: number,
  pageWidth: number,
  pageHeight: number,
): { x: number; y: number; w: number; h: number } {
  const off = itemMargin(item) + spacingPad(spacing);
  const x = Math.max(0, placement.x - off);
  const y = Math.max(0, placement.y - off);
  const r = Math.min(pageWidth, placement.x + placement.width + off);
  const b = Math.min(pageHeight, placement.y + placement.height + off);
  return { x, y, w: Math.max(0, r - x), h: Math.max(0, b - y) };
}

function validateInput(input: LayoutInput): void {
  if (!Number.isInteger(input.pageWidth) || !Number.isInteger(input.pageHeight) ||
      input.pageWidth <= 0 || input.pageHeight <= 0) {
    throw new Error('page dimensions must be positive integers');
  }
  if (!Number.isInteger(input.spacing) || input.spacing < 0) {
    throw new Error('spacing must be a non-negative integer');
  }
  const seen = new Set<string>();
  for (const item of input.items) {
    if (!Number.isInteger(item.width) || !Number.isInteger(item.height) ||
        item.width <= 0 || item.height <= 0) {
      throw new Error(`item ${item.id}: dimensions must be positive integers`);
    }
    if (seen.has(item.id)) throw new Error(`duplicate item id: ${item.id}`);
    seen.add(item.id);
  }
}

/** Does the item footprint fit the page at all (in some orientation)? */
export function fitsPage(input: LayoutInput, item: ItemSpec): boolean {
  const { fw, fh } = footprintSize(item, input.spacing);
  if (fw <= input.pageWidth && fh <= input.pageHeight) return true;
  return itemRotatable(item) && fh <= input.pageWidth && fw <= input.pageHeight;
}

/**
 * Deterministic heuristic pack. Fixed items are pinned first; movable items
 * follow a stable order into a MAXRECTS bin per page. Small instances are
 * re-solved exactly for an optimality certificate.
 */
export function pack(input: LayoutInput, opts: PackOptions = {}): PackResult {
  validateInput(input);
  const unpacked: UnpackedItem[] = [];
  const placements: Placement[] = [];

  // 1. Pin fixed items (deterministic id order); detect conflicts.
  const fixedItems = input.items
    .filter((it) => it.fixed)
    .sort((a, b) => compareIds(a.id, b.id));
  const fixedObstacles = new Map<number, FixedObstacle[]>();
  let fixedMaxPage = -1;
  for (const item of fixedItems) {
    const anchor = item.fixed!;
    const rotated = anchor.rotated ?? false;
    const width = rotated ? item.height : item.width;
    const height = rotated ? item.width : item.height;
    const bad =
      !Number.isInteger(anchor.x) || !Number.isInteger(anchor.y) ||
      !Number.isInteger(anchor.page) || anchor.page < 0 ||
      anchor.x < 0 || anchor.y < 0 ||
      anchor.x + width > input.pageWidth || anchor.y + height > input.pageHeight;
    if (bad) {
      unpacked.push({ id: item.id, reason: 'fixed-conflict' });
      continue;
    }
    const rect = footprintRectOf(
      item,
      { x: anchor.x, y: anchor.y, width, height },
      input.spacing,
      input.pageWidth,
      input.pageHeight,
    );
    const list = fixedObstacles.get(anchor.page) ?? [];
    const overlaps = list.some((o) =>
      o.x < rect.x + rect.w && rect.x < o.x + o.w && o.y < rect.y + rect.h && rect.y < o.y + o.h,
    );
    if (overlaps) {
      unpacked.push({ id: item.id, reason: 'fixed-conflict' });
      continue;
    }
    list.push({ page: anchor.page, ...rect });
    fixedObstacles.set(anchor.page, list);
    fixedMaxPage = Math.max(fixedMaxPage, anchor.page);
    placements.push({
      id: item.id, page: anchor.page, x: anchor.x, y: anchor.y,
      width, height, rotated,
    });
  }

  // 2. Reject movable items that can never fit — explicit failure, never overflow.
  const movable: ItemSpec[] = [];
  for (const item of input.items) {
    if (item.fixed) continue;
    if (!fitsPage(input, item)) unpacked.push({ id: item.id, reason: 'too-large' });
    else movable.push(item);
  }

  // 3. Heuristic MAXRECTS packing across pages.
  const pages: PagePacker[] = [];
  const pageFor = (p: number): PagePacker => {
    while (pages.length <= p) {
      const packer = new PagePacker(input.pageWidth, input.pageHeight);
      for (const ob of fixedObstacles.get(pages.length) ?? []) packer.addObstacle(ob);
      pages.push(packer);
    }
    return pages[p]!;
  };
  for (let p = 0; p <= fixedMaxPage; p++) pageFor(p);

  const heuristicPlacements: Placement[] = [];
  for (const item of packingOrder(movable, input.spacing)) {
    const { fw, fh } = footprintSize(item, input.spacing);
    // Try every open page first; only then grow the atlas by one page.
    let placed: Placement | null = null;
    for (let p = 0; p <= pages.length && !placed; p++) {
      const spot = pageFor(p).place(fw, fh, itemRotatable(item));
      if (spot) {
        const origin = spriteOrigin(spot.x, spot.y, item, input.spacing);
        placed = {
          id: item.id, page: p, x: origin.x, y: origin.y,
          width: spot.rotated ? item.height : item.width,
          height: spot.rotated ? item.width : item.height,
          rotated: spot.rotated,
        };
      }
    }
    // fitsPage() was checked up front, so a fresh page always succeeds.
    heuristicPlacements.push(placed!);
  }

  // 4. Exact certificate for small instances.
  const exactMaxItems = opts.exactMaxItems ?? 10;
  const lb = lowerBoundPages(input, movable, fixedMaxPage);
  let finalMovable = heuristicPlacements;
  let pagesUsed = Math.max(pages.length, fixedMaxPage + 1);
  let proof: PackResult['proof'];

  if (movable.length === 0) {
    proof = { status: 'optimal', pages: pagesUsed };
  } else if (movable.length <= exactMaxItems) {
    const exact = exactPack(input, movable, {
      lowerBound: lb,
      fixedObstacles,
      ...(opts.exactNodeLimit !== undefined ? { nodeLimit: opts.exactNodeLimit } : {}),
    });
    if (exact && exact.proven && exact.pages <= pagesUsed) {
      finalMovable = exact.placements;
      pagesUsed = Math.max(exact.pages, fixedMaxPage + 1);
      proof = { status: 'optimal', pages: pagesUsed };
    } else {
      proof = { status: 'heuristic', lowerBound: lb, upperBound: pagesUsed };
    }
  } else {
    proof = { status: 'heuristic', lowerBound: lb, upperBound: pagesUsed };
  }

  placements.push(...finalMovable);
  placements.sort((a, b) => compareIds(a.id, b.id));
  return { placements, unpacked, pages: pagesUsed, proof };
}
