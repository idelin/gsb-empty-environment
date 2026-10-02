import { footprintOf, normalizeMargin } from './types.ts';
import type { ItemSpec, Layout } from './types.ts';

export interface VerifyReport {
  ok: boolean;
  errors: string[];
}

/**
 * Independent pixel-occupancy verifier.
 *
 * For every page it allocates a real pixel grid and rasterizes each placed
 * footprint (margin ring + texture body). Any cell written twice is an
 * overlap; any cell outside the page is an out-of-bounds error. It also
 * re-checks placement metadata against the item specs (size, rotation
 * permission, margin, fixed position). This verifier shares no code with
 * the packer, so it catches packer bugs rather than echoing them.
 */
export function verifyLayout(layout: Layout, items: ItemSpec[]): VerifyReport {
  const errors: string[] = [];
  const specById = new Map(items.map((it) => [it.id, it]));
  const grids: Uint8Array[] = [];
  for (let p = 0; p < layout.pages; p++) {
    grids.push(new Uint8Array(layout.pageW * layout.pageH));
  }
  const seenIds = new Set<string>();

  for (const pl of layout.placements) {
    if (seenIds.has(pl.id)) {
      errors.push(`duplicate placement for id '${pl.id}'`);
      continue;
    }
    seenIds.add(pl.id);

    const spec = specById.get(pl.id);
    if (spec === undefined) {
      errors.push(`placement '${pl.id}' has no matching item spec`);
      continue;
    }
    // Cross-check metadata against the spec.
    const expectedW = pl.rotated ? spec.h : spec.w;
    const expectedH = pl.rotated ? spec.w : spec.h;
    if (pl.w !== expectedW || pl.h !== expectedH) {
      errors.push(
        `'${pl.id}' stored size ${pl.w}x${pl.h} inconsistent with spec ${spec.w}x${spec.h} rotated=${pl.rotated}`,
      );
    }
    if (pl.rotated && spec.rotatable === false) {
      errors.push(`'${pl.id}' is rotated but spec forbids rotation`);
    }
    const m = normalizeMargin(spec.margin);
    if (
      pl.margin.top !== m.top ||
      pl.margin.right !== m.right ||
      pl.margin.bottom !== m.bottom ||
      pl.margin.left !== m.left
    ) {
      errors.push(`'${pl.id}' margin mismatch with spec`);
    }
    if (spec.fixed !== undefined) {
      const f = spec.fixed;
      const fr = f.rotated === true && spec.w !== spec.h;
      if (pl.page !== f.page || pl.x !== f.x || pl.y !== f.y || pl.rotated !== fr) {
        errors.push(
          `fixed item '${pl.id}' moved from page ${f.page} (${f.x},${f.y}) rotated=${fr} ` +
            `to page ${pl.page} (${pl.x},${pl.y}) rotated=${pl.rotated}`,
        );
      }
    }
    if (pl.page < 0 || pl.page >= layout.pages) {
      errors.push(`'${pl.id}' references invalid page ${pl.page}`);
      continue;
    }

    // Rasterize the footprint pixel by pixel.
    const fp = footprintOf(pl);
    if (fp.x < 0 || fp.y < 0 || fp.x + fp.w > layout.pageW || fp.y + fp.h > layout.pageH) {
      errors.push(
        `'${pl.id}' footprint (${fp.x},${fp.y} ${fp.w}x${fp.h}) out of page ${pl.page} bounds ${layout.pageW}x${layout.pageH}`,
      );
      continue;
    }
    const grid = grids[pl.page]!;
    for (let yy = fp.y; yy < fp.y + fp.h; yy++) {
      for (let xx = fp.x; xx < fp.x + fp.w; xx++) {
        const idx = yy * layout.pageW + xx;
        if (grid[idx] !== 0) {
          errors.push(`overlap at page ${pl.page} pixel (${xx},${yy}) involving '${pl.id}'`);
          // Keep scanning this item but stop spamming per-pixel errors for it.
          yy = fp.y + fp.h;
          break;
        }
        grid[idx] = 1;
      }
    }
  }

  // Every non-unpacked spec must be placed exactly once.
  for (const spec of items) {
    const wasUnpacked = layout.unpacked.some((u) => u.id === spec.id);
    if (!seenIds.has(spec.id) && !wasUnpacked) {
      errors.push(`item '${spec.id}' is neither placed nor reported unpacked`);
    }
    if (seenIds.has(spec.id) && wasUnpacked) {
      errors.push(`item '${spec.id}' is both placed and reported unpacked`);
    }
  }
  return { ok: errors.length === 0, errors };
}

/** Convenience assertion for tests and callers. */
export function assertLayoutValid(layout: Layout, items: ItemSpec[]): void {
  const report = verifyLayout(layout, items);
  if (!report.ok) {
    throw new Error(`layout verification failed:\n  ${report.errors.join('\n  ')}`);
  }
}
