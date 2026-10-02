import { RGBAImage, colorForId } from './rgba.ts';
import {
  itemMargin,
  itemRotatable,
  spacingPad,
} from './geometry.ts';
import type { ItemSpec, Placement } from './types.ts';

export interface VerifyResult {
  ok: boolean;
  errors: string[];
}

/**
 * Independent layout verifier. It shares no state with the packer: every
 * placement is rasterized into a fresh per-page RGBA occupancy image and any
 * doubly-claimed pixel is an overlap. Because each item paints its full
 * footprint (sprite + own margin + its half of the spacing), non-overlap of
 * footprints proves the required separation:
 *   gap(spriteA, spriteB) >= marginA + marginB + spacing.
 */
export function verifyLayout(
  pageWidth: number,
  pageHeight: number,
  spacing: number,
  items: ItemSpec[],
  placements: Placement[],
): VerifyResult {
  const errors: string[] = [];
  const byId = new Map(items.map((it) => [it.id, it]));
  const seen = new Set<string>();
  const pages = new Map<number, RGBAImage>();
  const pad = spacingPad(spacing);

  const pageImage = (p: number): RGBAImage => {
    let img = pages.get(p);
    if (!img) {
      img = new RGBAImage(pageWidth, pageHeight);
      pages.set(p, img);
    }
    return img;
  };

  for (const pl of placements) {
    if (seen.has(pl.id)) {
      errors.push(`duplicate placement for ${pl.id}`);
      continue;
    }
    seen.add(pl.id);
    const item = byId.get(pl.id);
    if (!item) {
      errors.push(`placement for unknown item ${pl.id}`);
      continue;
    }
    // Rotation consistency: placed size must be a (possibly swapped) texture size.
    const normal = pl.width === item.width && pl.height === item.height;
    const swapped = pl.width === item.height && pl.height === item.width;
    if (pl.rotated && !swapped) errors.push(`${pl.id}: rotated but size not swapped`);
    if (!pl.rotated && !normal) errors.push(`${pl.id}: size mismatch`);
    if (pl.rotated && !itemRotatable(item)) errors.push(`${pl.id}: rotation not allowed`);
    // Fixed items must sit exactly at their anchor.
    if (item.fixed) {
      const a = item.fixed;
      if (pl.page !== a.page || pl.x !== a.x || pl.y !== a.y || pl.rotated !== (a.rotated ?? false)) {
        errors.push(`${pl.id}: fixed item moved from its anchor`);
      }
    }
    // Bounds: the actual sprite must be fully inside its page.
    if (pl.page < 0 || pl.x < 0 || pl.y < 0 ||
        pl.x + pl.width > pageWidth || pl.y + pl.height > pageHeight) {
      errors.push(`${pl.id}: sprite out of page bounds`);
      continue;
    }
    // Pixel occupancy of the footprint (clipped at page edges).
    const off = itemMargin(item) + pad;
    const fx = Math.max(0, pl.x - off);
    const fy = Math.max(0, pl.y - off);
    const fr = Math.min(pageWidth, pl.x + pl.width + off);
    const fb = Math.min(pageHeight, pl.y + pl.height + off);
    const clashes = pageImage(pl.page).fillRect(fx, fy, fr - fx, fb - fy, colorForId(pl.id));
    if (clashes > 0) {
      errors.push(`${pl.id}: footprint overlaps ${clashes} pixel(s) on page ${pl.page}`);
    }
  }

  // Every non-failed item should be placed exactly once (checked by caller
  // via unpacked list; here we only flag placements for missing items).
  return { ok: errors.length === 0, errors };
}

/**
 * Pairwise analytic cross-check (independent of the rasterizer): verifies
 * the required separation directly from coordinates.
 */
export function verifySeparation(
  spacing: number,
  items: ItemSpec[],
  placements: Placement[],
): VerifyResult {
  const errors: string[] = [];
  const byId = new Map(items.map((it) => [it.id, it]));
  const byPage = new Map<number, Placement[]>();
  for (const pl of placements) {
    const list = byPage.get(pl.page) ?? [];
    list.push(pl);
    byPage.set(pl.page, list);
  }
  for (const [page, list] of byPage) {
    for (let i = 0; i < list.length; i++) {
      for (let j = i + 1; j < list.length; j++) {
        const a = list[i]!;
        const b = list[j]!;
        const ia = byId.get(a.id)!;
        const ib = byId.get(b.id)!;
        const need = itemMargin(ia) + itemMargin(ib) + spacing;
        const dx = Math.max(0, Math.max(a.x - (b.x + b.width), b.x - (a.x + a.width)));
        const dy = Math.max(0, Math.max(a.y - (b.y + b.height), b.y - (a.y + a.height)));
        const gap = Math.max(dx, dy) === 0 ? 0 : Math.min(dx === 0 ? Infinity : dx, dy === 0 ? Infinity : dy);
        const separated = dx >= need || dy >= need;
        if (!separated) {
          errors.push(
            `${a.id} vs ${b.id} on page ${page}: gap ${String(gap)} < required ${need}`,
          );
        }
      }
    }
  }
  return { ok: errors.length === 0, errors };
}

/** Convenience: both independent checks must pass. */
export function verifyAll(
  pageWidth: number,
  pageHeight: number,
  spacing: number,
  items: ItemSpec[],
  placements: Placement[],
): VerifyResult {
  const raster = verifyLayout(pageWidth, pageHeight, spacing, items, placements);
  const analytic = verifySeparation(spacing, items, placements);
  return {
    ok: raster.ok && analytic.ok,
    errors: [...raster.errors, ...analytic.errors],
  };
}
