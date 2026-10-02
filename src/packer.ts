import {
  normalizeMargin,
  isPosInt,
  isNonNegInt,
  footprintOf,
} from './types.ts';
import type {
  ItemSpec,
  Layout,
  Placement,
  UnpackedItem,
  Footprint,
} from './types.ts';

interface FreeRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

interface Candidate {
  page: number;
  x: number;
  y: number;
  fw: number;
  fh: number;
  rotated: boolean;
  shortSideFit: number;
  longSideFit: number;
}

function rectsOverlap(a: FreeRect, b: FreeRect): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

function containsRect(outer: FreeRect, inner: FreeRect): boolean {
  return (
    inner.x >= outer.x &&
    inner.y >= outer.y &&
    inner.x + inner.w <= outer.x + outer.w &&
    inner.y + inner.h <= outer.y + outer.h
  );
}

/** Subtract `used` from every free rect, splitting into up to 4 rects. */
function subtractFromAll(free: FreeRect[], used: Footprint): FreeRect[] {
  const out: FreeRect[] = [];
  for (const r of free) {
    if (!rectsOverlap(r, used)) {
      out.push(r);
      continue;
    }
    // Left slice
    if (used.x > r.x) {
      out.push({ x: r.x, y: r.y, w: used.x - r.x, h: r.h });
    }
    // Right slice
    if (used.x + used.w < r.x + r.w) {
      out.push({ x: used.x + used.w, y: r.y, w: r.x + r.w - (used.x + used.w), h: r.h });
    }
    // Top slice
    if (used.y > r.y) {
      out.push({ x: r.x, y: r.y, w: r.w, h: used.y - r.y });
    }
    // Bottom slice
    if (used.y + used.h < r.y + r.h) {
      out.push({ x: r.x, y: used.y + used.h, w: r.w, h: r.y + r.h - (used.y + used.h) });
    }
  }
  return prune(out);
}

/** Drop zero-area rects and rects fully contained in another (deterministic order kept). */
function prune(free: FreeRect[]): FreeRect[] {
  const nonEmpty = free.filter((r) => r.w > 0 && r.h > 0);
  const out: FreeRect[] = [];
  for (let i = 0; i < nonEmpty.length; i++) {
    const ri = nonEmpty[i]!;
    let contained = false;
    for (let j = 0; j < nonEmpty.length; j++) {
      if (i === j) continue;
      const rj = nonEmpty[j]!;
      if (containsRect(rj, ri)) {
        // Keep the earlier copy on exact duplicates for determinism.
        if (j < i || !sameRect(ri, rj)) {
          contained = true;
          break;
        }
      }
    }
    if (!contained) out.push(ri);
  }
  return out;
}

function sameRect(a: FreeRect, b: FreeRect): boolean {
  return a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;
}

export interface ValidatedItem {
  spec: ItemSpec;
  margin: { top: number; right: number; bottom: number; left: number };
  rotatable: boolean;
}

export function validateItems(
  items: ItemSpec[],
): { valid: ValidatedItem[]; invalid: UnpackedItem[] } {
  const valid: ValidatedItem[] = [];
  const invalid: UnpackedItem[] = [];
  const seen = new Set<string>();
  for (const spec of items) {
    const fail = (detail: string) => invalid.push({ id: spec.id, reason: 'invalid', detail });
    if (seen.has(spec.id)) {
      fail(`duplicate id '${spec.id}'`);
      continue;
    }
    seen.add(spec.id);
    if (!isPosInt(spec.w) || !isPosInt(spec.h)) {
      fail(`texture size must be positive integers, got ${spec.w}x${spec.h}`);
      continue;
    }
    const margin = normalizeMargin(spec.margin);
    if (
      !isNonNegInt(margin.top) ||
      !isNonNegInt(margin.right) ||
      !isNonNegInt(margin.bottom) ||
      !isNonNegInt(margin.left)
    ) {
      fail('margin sides must be non-negative integers');
      continue;
    }
    if (spec.fixed !== undefined) {
      const f = spec.fixed;
      if (!isNonNegInt(f.page) || !isNonNegInt(f.x) || !isNonNegInt(f.y)) {
        fail('fixed placement must use non-negative integer page/x/y');
        continue;
      }
      if (f.rotated === true && spec.rotatable === false) {
        fail('fixed placement is rotated but item forbids rotation');
        continue;
      }
      if (f.rotated === true && spec.w === spec.h) {
        // Rotating a square is a no-op; accept but normalize to not-rotated.
        valid.push({
          spec: { ...spec, fixed: { page: f.page, x: f.x, y: f.y } },
          margin,
          rotatable: spec.rotatable ?? true,
        });
        continue;
      }
    }
    valid.push({ spec, margin, rotatable: spec.rotatable ?? true });
  }
  return { valid, invalid };
}

/** Stored (on-page) size of an item for a given rotation flag. */
export function storedSize(
  item: ValidatedItem,
  rotated: boolean,
): { w: number; h: number } {
  const { w, h } = item.spec;
  return rotated ? { w: h, h: w } : { w, h };
}

/** Footprint size (stored size + margin) for a rotation flag. */
export function footprintSize(
  item: ValidatedItem,
  rotated: boolean,
): { w: number; h: number } {
  const s = storedSize(item, rotated);
  return {
    w: s.w + item.margin.left + item.margin.right,
    h: s.h + item.margin.top + item.margin.bottom,
  };
}

/** Orientations to try, in deterministic preference order. */
export function orientations(item: ValidatedItem): boolean[] {
  if (!item.rotatable || item.spec.w === item.spec.h) return [false];
  return [false, true];
}

/**
 * Deterministic MaxRects (best-short-side-fit) packer.
 * Fixed items are committed first; the rest are placed largest-first with
 * total-order tie-breaking, so identical inputs always give identical output.
 */
export function packHeuristic(
  items: ValidatedItem[],
  pageW: number,
  pageH: number,
  initialPages: FreeRect[][] = [],
): { placements: Placement[]; unpacked: UnpackedItem[]; free: FreeRect[][] } {
  const free: FreeRect[][] = initialPages.map((p) => p.map((r) => ({ ...r })));
  const placements: Placement[] = [];
  const unpacked: UnpackedItem[] = [];

  const ensurePage = (idx: number) => {
    while (free.length <= idx) free.push([{ x: 0, y: 0, w: pageW, h: pageH }]);
  };

  // 1. Commit fixed items exactly where the caller put them.
  const fixedItems = items.filter((it) => it.spec.fixed !== undefined);
  for (const item of fixedItems) {
    const f = item.spec.fixed!;
    const rotated = f.rotated === true;
    const stored = storedSize(item, rotated);
    const placement: Placement = {
      id: item.spec.id,
      page: f.page,
      x: f.x,
      y: f.y,
      w: stored.w,
      h: stored.h,
      rotated,
      margin: item.margin,
    };
    const fp = footprintOf(placement);
    if (fp.x < 0 || fp.y < 0 || fp.x + fp.w > pageW || fp.y + fp.h > pageH) {
      unpacked.push({
        id: item.spec.id,
        reason: 'invalid',
        detail: `fixed footprint ${fp.w}x${fp.h} at (${fp.x},${fp.y}) exceeds page ${pageW}x${pageH}`,
      });
      continue;
    }
    ensurePage(f.page);
    free[f.page] = subtractFromAll(free[f.page]!, fp);
    placements.push(placement);
  }

  // 2. Sort movable items: max footprint side desc, min side desc, area desc, id asc.
  const movable = items
    .filter((it) => it.spec.fixed === undefined)
    .map((it) => {
      const sizes = orientations(it).map((r) => footprintSize(it, r));
      const maxSide = Math.max(...sizes.map((s) => Math.max(s.w, s.h)));
      const minSide = Math.min(...sizes.map((s) => Math.min(s.w, s.h)));
      return { it, maxSide, minSide, area: sizes[0]!.w * sizes[0]!.h };
    });
  movable.sort((a, b) => {
    if (a.maxSide !== b.maxSide) return b.maxSide - a.maxSide;
    if (a.minSide !== b.minSide) return b.minSide - a.minSide;
    if (a.area !== b.area) return b.area - a.area;
    return a.it.spec.id < b.it.spec.id ? -1 : a.it.spec.id > b.it.spec.id ? 1 : 0;
  });

  // 3. Place each item at the best free position across all open pages.
  for (const { it } of movable) {
    let best: Candidate | null = null;
    for (let page = 0; page < free.length; page++) {
      for (const r of free[page]!) {
        for (const rotated of orientations(it)) {
          const fs = footprintSize(it, rotated);
          if (fs.w > r.w || fs.h > r.h) continue;
          const shortSideFit = Math.min(r.w - fs.w, r.h - fs.h);
          const longSideFit = Math.max(r.w - fs.w, r.h - fs.h);
          const cand: Candidate = {
            page,
            x: r.x,
            y: r.y,
            fw: fs.w,
            fh: fs.h,
            rotated,
            shortSideFit,
            longSideFit,
          };
          if (best === null || betterCandidate(cand, best)) best = cand;
        }
      }
    }
    if (best === null) {
      // Does it fit on a fresh page at all? If not, it is explicitly too large.
      const fitsEmpty = orientations(it).some((r) => {
        const fs = footprintSize(it, r);
        return fs.w <= pageW && fs.h <= pageH;
      });
      if (!fitsEmpty) {
        const fs = footprintSize(it, false);
        unpacked.push({
          id: it.spec.id,
          reason: 'too-large',
          detail: `footprint ${fs.w}x${fs.h} (incl. margin) exceeds page ${pageW}x${pageH} in every allowed orientation`,
        });
        continue;
      }
      // Open a new page and place at its origin (guaranteed to fit).
      const page = free.length;
      ensurePage(page);
      const rotated = orientations(it).find((r) => {
        const fs = footprintSize(it, r);
        return fs.w <= pageW && fs.h <= pageH;
      })!;
      const fs = footprintSize(it, rotated);
      best = { page, x: 0, y: 0, fw: fs.w, fh: fs.h, rotated, shortSideFit: 0, longSideFit: 0 };
    }

    const stored = storedSize(it, best.rotated);
    const placement: Placement = {
      id: it.spec.id,
      page: best.page,
      x: best.x + it.margin.left,
      y: best.y + it.margin.top,
      w: stored.w,
      h: stored.h,
      rotated: best.rotated,
      margin: it.margin,
    };
    placements.push(placement);
    free[best.page] = subtractFromAll(free[best.page]!, {
      x: best.x,
      y: best.y,
      w: best.fw,
      h: best.fh,
    });
  }

  return { placements, unpacked, free };
}

/** Total order on candidates: short-side fit, long-side fit, page, y, x, unrotated first. */
function betterCandidate(a: Candidate, b: Candidate): boolean {
  if (a.shortSideFit !== b.shortSideFit) return a.shortSideFit < b.shortSideFit;
  if (a.longSideFit !== b.longSideFit) return a.longSideFit < b.longSideFit;
  if (a.page !== b.page) return a.page < b.page;
  if (a.y !== b.y) return a.y < b.y;
  if (a.x !== b.x) return a.x < b.x;
  return a.rotated === false && b.rotated === true;
}

export function layoutFrom(
  pageW: number,
  pageH: number,
  placements: Placement[],
  unpacked: UnpackedItem[],
): Layout {
  const pages = placements.reduce((m, p) => Math.max(m, p.page + 1), 0);
  return { pageW, pageH, pages, placements, unpacked };
}
