/**
 * Palette selection, deliberately separated from error diffusion.
 *
 * Deterministic median cut over the opaque pixels of the image, in u8 RGB.
 * Transparent pixels (alpha === 0) never enter the fit, so they never
 * consume any of the fixed opaque-color budget.
 *
 * Determinism: no randomness, no wall-clock, fully specified tie-breaks.
 */

import type { Palette, RGB, RGBAImage } from "./types.ts";

interface Box {
  /** Indices into `colors`, sorted lazily per split. */
  members: number[];
}

function channelRange(colors: RGB[], members: number[], ch: number): number {
  let lo = Infinity;
  let hi = -Infinity;
  for (const m of members) {
    const v = colors[m][ch];
    if (v < lo) lo = v;
    if (v > hi) hi = v;
  }
  return hi - lo;
}

function meanColor(colors: RGB[], members: number[]): RGB {
  let r = 0;
  let g = 0;
  let b = 0;
  for (const m of members) {
    r += colors[m][0];
    g += colors[m][1];
    b += colors[m][2];
  }
  const n = members.length;
  return [Math.round(r / n), Math.round(g / n), Math.round(b / n)];
}

/** Collect opaque pixel colors in raster order (deterministic). */
export function opaqueColors(image: RGBAImage): RGB[] {
  const out: RGB[] = [];
  const { data } = image;
  for (let i = 0; i < data.length; i += 4) {
    if (data[i + 3] !== 0) {
      out.push([data[i], data[i + 1], data[i + 2]]);
    }
  }
  return out;
}

/**
 * Build a palette of at most `colorCount` opaque colors.
 *
 * Defined edge cases:
 * - colorCount must be an integer >= 1 (colorCount === 1 yields the mean
 *   of all opaque colors).
 * - An image with no opaque pixels yields an empty palette.
 * - Fewer distinct colors than requested yields a smaller palette.
 */
export function buildPalette(image: RGBAImage, colorCount: number): Palette {
  if (!Number.isInteger(colorCount) || colorCount < 1) {
    throw new Error(`colorCount must be an integer >= 1, got ${colorCount}`);
  }
  const colors = opaqueColors(image);
  if (colors.length === 0) {
    return { colors: [] };
  }

  let boxes: Box[] = [{ members: colors.map((_, i) => i) }];

  while (boxes.length < colorCount) {
    // Pick the box with the widest channel range; ties broken by larger
    // member count, then by earliest position in the list.
    let best = -1;
    let bestRange = 0;
    let bestCh = 0;
    for (let bi = 0; bi < boxes.length; bi++) {
      const members = boxes[bi].members;
      if (members.length < 2) continue;
      for (let ch = 0; ch < 3; ch++) {
        const range = channelRange(colors, members, ch);
        const better =
          range > bestRange ||
          (range === bestRange && range > 0 && best >= 0 && members.length > boxes[best].members.length);
        if (range > 0 && (best < 0 || better)) {
          best = bi;
          bestRange = range;
          bestCh = ch;
        }
      }
    }
    if (best < 0) break; // No splittable box remains.

    const members = boxes[best].members.slice();
    // Total deterministic order: split channel, then r, g, b, then index.
    members.sort((a, b) => {
      const ca = colors[a];
      const cb = colors[b];
      return (
        ca[bestCh] - cb[bestCh] ||
        ca[0] - cb[0] ||
        ca[1] - cb[1] ||
        ca[2] - cb[2] ||
        a - b
      );
    });
    const mid = Math.floor((members.length - 1) / 2);
    const left = members.slice(0, mid + 1);
    const right = members.slice(mid + 1);
    boxes = [...boxes.slice(0, best), { members: left }, { members: right }, ...boxes.slice(best + 1)];
  }

  // Box mean, then dedupe preserving order (a duplicated mean would waste budget).
  const seen = new Set<number>();
  const palette: RGB[] = [];
  for (const box of boxes) {
    const c = meanColor(colors, box.members);
    const key = (c[0] << 16) | (c[1] << 8) | c[2];
    if (!seen.has(key)) {
      seen.add(key);
      palette.push(c);
    }
  }
  return { colors: palette };
}
