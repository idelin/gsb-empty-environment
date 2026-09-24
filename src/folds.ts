/**
 * Fold-region bookkeeping: normalization, validation and tree building.
 *
 * A set of fold regions is well-formed when every pair is either disjoint or
 * properly nested. Partially intersecting regions (a.start < b.start < a.end
 * < b.end) are illegal: callers must either reject them (validateFolds /
 * FoldedDocument.addFold) or normalize them (normalizeFolds merges each
 * intersecting chain into its union).
 */

export interface FoldRegion {
  /** Start offset in the original text (UTF-16 code units, inclusive). */
  start: number;
  /** End offset in the original text (UTF-16 code units, exclusive). */
  end: number;
}

export function overlapsPartially(a: FoldRegion, b: FoldRegion): boolean {
  return (
    (a.start < b.start && b.start < a.end && a.end < b.end) ||
    (b.start < a.start && a.start < b.end && b.end < a.end)
  );
}

export function intersects(a: FoldRegion, b: FoldRegion): boolean {
  return a.start < b.end && b.start < a.end;
}

export function contains(outer: FoldRegion, inner: FoldRegion): boolean {
  return outer.start <= inner.start && inner.end <= outer.end;
}

function sortRegions(regions: FoldRegion[]): FoldRegion[] {
  return regions.sort((a, b) => a.start - b.start || b.end - a.end);
}

/**
 * Normalize a list of regions into a well-formed set: empty regions and exact
 * duplicates are dropped, and every chain of partially intersecting regions is
 * merged into its union (fixpoint). The result is sorted by (start asc,
 * end desc), so nested regions directly follow their parent.
 */
export function normalizeFolds(regions: FoldRegion[]): FoldRegion[] {
  const list = sortRegions(
    regions
      .filter((r) => r.end > r.start)
      .map((r) => ({ start: r.start, end: r.end })),
  );

  // Drop exact duplicates.
  for (let i = list.length - 1; i > 0; i--) {
    if (list[i].start === list[i - 1].start && list[i].end === list[i - 1].end) {
      list.splice(i, 1);
    }
  }

  // Merge partially intersecting pairs until none remain.
  let changed = true;
  while (changed) {
    changed = false;
    for (let i = 0; i < list.length && !changed; i++) {
      for (let j = i + 1; j < list.length && !changed; j++) {
        if (overlapsPartially(list[i], list[j])) {
          list[i] = {
            start: Math.min(list[i].start, list[j].start),
            end: Math.max(list[i].end, list[j].end),
          };
          list.splice(j, 1);
          sortRegions(list);
          changed = true;
        }
      }
    }
  }
  return list;
}

/**
 * Throw when the regions are not well-formed (partial intersection).
 * Returns the regions sorted by (start asc, end desc) when valid.
 */
export function validateFolds(regions: FoldRegion[]): FoldRegion[] {
  const sorted = sortRegions([...regions]);
  for (let i = 0; i < sorted.length; i++) {
    for (let j = i + 1; j < sorted.length; j++) {
      const a = sorted[i];
      const b = sorted[j];
      if (b.start >= a.end) break; // sorted: nothing later can intersect a
      if (overlapsPartially(a, b)) {
        throw new Error(
          `Fold regions partially intersect: [${a.start}, ${a.end}) vs ` +
            `[${b.start}, ${b.end}). Normalize or remove one of them.`,
        );
      }
    }
  }
  return sorted;
}

export interface FoldNode {
  start: number;
  end: number;
  collapsed: boolean;
  children: FoldNode[];
}

/**
 * Build a nesting forest from well-formed regions. `collapsed[i]` optionally
 * carries the collapsed flag for the i-th region in sorted order.
 */
export function buildFoldTree(
  regions: FoldRegion[],
  collapsed?: boolean[],
): FoldNode[] {
  const sorted = validateFolds(regions);
  const roots: FoldNode[] = [];
  const stack: FoldNode[] = [];
  for (let i = 0; i < sorted.length; i++) {
    const region = sorted[i];
    const node: FoldNode = {
      start: region.start,
      end: region.end,
      collapsed: collapsed ? (collapsed[i] ?? true) : true,
      children: [],
    };
    while (stack.length > 0 && stack[stack.length - 1].end <= region.start) {
      stack.pop();
    }
    if (stack.length === 0) {
      roots.push(node);
    } else {
      stack[stack.length - 1].children.push(node);
    }
    stack.push(node);
  }
  return roots;
}

/** Depth-first flattening of a fold forest (parents before children). */
export function flattenFoldTree(roots: FoldNode[]): FoldNode[] {
  const out: FoldNode[] = [];
  const walk = (nodes: FoldNode[]) => {
    for (const node of nodes) {
      out.push(node);
      walk(node.children);
    }
  };
  walk(roots);
  return out;
}
