import type { Layout, Placement } from './types.ts';

/** Minimal RGBA image: 4 bytes per pixel, row-major, no external libraries. */
export interface RgbaImage {
  w: number;
  h: number;
  data: Uint8Array;
}

export function createImage(w: number, h: number, fill: [number, number, number, number] = [0, 0, 0, 0]): RgbaImage {
  const data = new Uint8Array(w * h * 4);
  for (let i = 0; i < w * h; i++) {
    data[i * 4] = fill[0];
    data[i * 4 + 1] = fill[1];
    data[i * 4 + 2] = fill[2];
    data[i * 4 + 3] = fill[3];
  }
  return { w, h, data };
}

export function getPixel(img: RgbaImage, x: number, y: number): [number, number, number, number] {
  const i = (y * img.w + x) * 4;
  return [img.data[i]!, img.data[i + 1]!, img.data[i + 2]!, img.data[i + 3]!];
}

export function setPixel(img: RgbaImage, x: number, y: number, px: readonly [number, number, number, number]): void {
  const i = (y * img.w + x) * 4;
  img.data[i] = px[0];
  img.data[i + 1] = px[1];
  img.data[i + 2] = px[2];
  img.data[i + 3] = px[3];
}

/**
 * Map a source texture pixel (sx, sy) to its on-page offset for a placement.
 * Rotation is 90 degrees clockwise: stored size is (srcH x srcW) and
 * (sx, sy) -> (srcH - 1 - sy, sx).
 */
export function rotatedOffset(sx: number, sy: number, srcW: number, srcH: number, rotated: boolean): { dx: number; dy: number } {
  if (!rotated) return { dx: sx, dy: sy };
  return { dx: srcH - 1 - sy, dy: sx };
}

/** Blit a source texture into a page image at the placement position. */
export function blit(page: RgbaImage, src: RgbaImage, placement: Placement): void {
  for (let sy = 0; sy < src.h; sy++) {
    for (let sx = 0; sx < src.w; sx++) {
      const { dx, dy } = rotatedOffset(sx, sy, src.w, src.h, placement.rotated);
      const tx = placement.x + dx;
      const ty = placement.y + dy;
      if (tx < 0 || ty < 0 || tx >= page.w || ty >= page.h) {
        throw new Error(`blit of '${placement.id}' writes outside page at (${tx},${ty})`);
      }
      setPixel(page, tx, ty, getPixel(src, sx, sy));
    }
  }
}

/** Render every page of a layout into RGBA images from source textures. */
export function renderPages(layout: Layout, sources: Map<string, RgbaImage>): RgbaImage[] {
  const pages: RgbaImage[] = [];
  for (let p = 0; p < layout.pages; p++) pages.push(createImage(layout.pageW, layout.pageH));
  for (const pl of layout.placements) {
    const src = sources.get(pl.id);
    if (src === undefined) throw new Error(`no source image for '${pl.id}'`);
    blit(pages[pl.page]!, src, pl);
  }
  return pages;
}

export interface UvPoint {
  u: number;
  v: number;
}

/**
 * UV coordinates (0..1, origin top-left) of the four source-texture corners
 * in order TL, TR, BR, BL, mapped through the placement's rotation.
 * Correct UVs let a renderer sample the atlas without re-rotating geometry.
 */
export function uvCorners(placement: Placement, pageW: number, pageH: number): [UvPoint, UvPoint, UvPoint, UvPoint] {
  const { x, y, w, h, rotated } = placement;
  // Stored rect corners in page space.
  const tl = { px: x, py: y };
  const tr = { px: x + w, py: y };
  const br = { px: x + w, py: y + h };
  const bl = { px: x, py: y + h };
  // Under 90-degree CW rotation, source corner TL lands at stored TR,
  // TR at BR, BR at BL, BL at TL.
  const corners = rotated ? [tr, br, bl, tl] : [tl, tr, br, bl];
  return corners.map((c) => ({ u: c.px / pageW, v: c.py / pageH })) as [UvPoint, UvPoint, UvPoint, UvPoint];
}

/**
 * Independent image-level check: rebuild the expected page content from the
 * source textures and UV mapping, and compare every texture pixel against
 * the rendered pages. Proves end-to-end that rotation and UVs are coherent.
 */
export function verifyRenderedPages(
  layout: Layout,
  sources: Map<string, RgbaImage>,
  pages: RgbaImage[],
): string[] {
  const errors: string[] = [];
  for (const pl of layout.placements) {
    const src = sources.get(pl.id);
    if (src === undefined) {
      errors.push(`no source image for '${pl.id}'`);
      continue;
    }
    const page = pages[pl.page];
    if (page === undefined) {
      errors.push(`'${pl.id}' placed on missing page ${pl.page}`);
      continue;
    }
    // 1. Every source pixel must appear at the rotated destination.
    for (let sy = 0; sy < src.h; sy++) {
      for (let sx = 0; sx < src.w; sx++) {
        const { dx, dy } = rotatedOffset(sx, sy, src.w, src.h, pl.rotated);
        const got = getPixel(page, pl.x + dx, pl.y + dy);
        const want = getPixel(src, sx, sy);
        if (got[0] !== want[0] || got[1] !== want[1] || got[2] !== want[2] || got[3] !== want[3]) {
          errors.push(
            `'${pl.id}' pixel mismatch at src (${sx},${sy}) -> page (${pl.x + dx},${pl.y + dy})`,
          );
          sy = src.h; // one error per item is enough
          break;
        }
      }
    }
    // 2. UV corners must point at the expected source corner texels.
    const uvs = uvCorners(pl, layout.pageW, layout.pageH);
    const srcCorners: Array<[number, number]> = [
      [0, 0],
      [src.w - 1, 0],
      [src.w - 1, src.h - 1],
      [0, src.h - 1],
    ];
    for (let c = 0; c < 4; c++) {
      const uv = uvs[c]!;
      // Convert the UV back to a texel: the corner UV sits on the far edge of
      // the corner texel, so step half a texel inward.
      const texelX = clamp(Math.floor(uv.u * page.w + inward(uv.u * page.w, pl.x, pl.x + pl.w)), 0, page.w - 1);
      const texelY = clamp(Math.floor(uv.v * page.h + inward(uv.v * page.h, pl.y, pl.y + pl.h)), 0, page.h - 1);
      const got = getPixel(page, texelX, texelY);
      const want = getPixel(src, srcCorners[c]![0], srcCorners[c]![1]);
      if (got[0] !== want[0] || got[1] !== want[1] || got[2] !== want[2] || got[3] !== want[3]) {
        errors.push(`'${pl.id}' UV corner ${c} does not sample source corner texel`);
      }
    }
  }
  return errors;
}

/** Step 0.5 texel inward from a corner coordinate toward the rect interior. */
function inward(coord: number, lo: number, hi: number): number {
  if (coord <= lo) return 0.5;
  if (coord >= hi) return -0.5;
  return 0;
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}
