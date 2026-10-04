import { makePlane, type Plane } from './types.ts';

/** Minimal self-contained RGBA image (no canvas, no external libs). */
export interface RgbaImage {
  width: number;
  height: number;
  data: Uint8ClampedArray; // RGBA, row-major
}

export type Rgba = readonly [number, number, number, number];

export function makeRgba(width: number, height: number, fill: Rgba = [0, 0, 0, 255]): RgbaImage {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let i = 0; i < width * height; i++) {
    data[i * 4] = fill[0];
    data[i * 4 + 1] = fill[1];
    data[i * 4 + 2] = fill[2];
    data[i * 4 + 3] = fill[3];
  }
  return { width, height, data };
}

/** Build an RGBA image from ASCII art plus a char -> colour palette. */
export function rgbaFromAscii(rows: string[], palette: Record<string, Rgba>): RgbaImage {
  const height = rows.length;
  const width = rows[0]?.length ?? 0;
  const img = makeRgba(width, height);
  for (let y = 0; y < height; y++) {
    if (rows[y].length !== width) throw new Error('ragged ascii rows');
    for (let x = 0; x < width; x++) {
      const c = palette[rows[y][x]];
      if (!c) throw new Error(`no palette entry for '${rows[y][x]}'`);
      img.data.set(c, (y * width + x) * 4);
    }
  }
  return img;
}

/** Render an RGBA image back to ASCII using the inverse palette. */
export function rgbaToAscii(img: RgbaImage, palette: Record<string, Rgba>, fallback = '?'): string[] {
  const inverse = new Map<string, string>();
  for (const [ch, c] of Object.entries(palette)) inverse.set(c.join(','), ch);
  const rows: string[] = [];
  for (let y = 0; y < img.height; y++) {
    let row = '';
    for (let x = 0; x < img.width; x++) {
      const i = (y * img.width + x) * 4;
      const key = `${img.data[i]},${img.data[i + 1]},${img.data[i + 2]},${img.data[i + 3]}`;
      row += inverse.get(key) ?? fallback;
    }
    rows.push(row);
  }
  return rows;
}

/** Extract a binary plane: channel value >= threshold becomes foreground. */
export function planeFromChannel(img: RgbaImage, channel: 0 | 1 | 2 | 3, threshold = 128): Plane {
  const plane = makePlane(img.width, img.height);
  for (let i = 0; i < img.width * img.height; i++) {
    plane.data[i] = img.data[i * 4 + channel] >= threshold ? 1 : 0;
  }
  return plane;
}

/** Extract a binary plane from luminance (Rec. 601), ignoring fully transparent pixels. */
export function planeFromLuminance(img: RgbaImage, threshold = 128): Plane {
  const plane = makePlane(img.width, img.height);
  for (let i = 0; i < img.width * img.height; i++) {
    const a = img.data[i * 4 + 3];
    const lum = 0.299 * img.data[i * 4] + 0.587 * img.data[i * 4 + 1] + 0.114 * img.data[i * 4 + 2];
    plane.data[i] = a >= 128 && lum >= threshold ? 1 : 0;
  }
  return plane;
}

/** Paint a binary plane onto an RGBA image (foreground -> fg, background untouched). */
export function overlayPlane(img: RgbaImage, plane: Plane, fg: Rgba): RgbaImage {
  const out: RgbaImage = { width: img.width, height: img.height, data: new Uint8ClampedArray(img.data) };
  for (let i = 0; i < plane.data.length; i++) {
    if (plane.data[i] > 0) out.data.set(fg, i * 4);
  }
  return out;
}

/** ASCII view of a binary plane ('#'/'.'), handy in tests and the demo. */
export function planeToAscii(plane: Plane): string[] {
  const rows: string[] = [];
  for (let y = 0; y < plane.height; y++) {
    let row = '';
    for (let x = 0; x < plane.width; x++) row += plane.data[y * plane.width + x] > 0 ? '#' : '.';
    rows.push(row);
  }
  return rows;
}

export function planeFromAscii(rows: string[], fg = '#'): Plane {
  const height = rows.length;
  const width = rows[0]?.length ?? 0;
  const plane = makePlane(width, height);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) plane.data[y * width + x] = rows[y][x] === fg ? 1 : 0;
  return plane;
}
