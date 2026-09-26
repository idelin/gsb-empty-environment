import type { Label } from "./raster.ts";

export type Rgba = readonly [number, number, number, number];

export interface RgbaImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8Array;
}

export function rgbaToLabels(img: RgbaImage, palette: readonly Rgba[]): Label[] {
  const lookup = new Map<number, Label>();
  palette.forEach((c, i) => lookup.set((c[0] << 24) | (c[1] << 16) | (c[2] << 8) | c[3], i));
  const out: Label[] = new Array(img.width * img.height);
  for (let p = 0; p < out.length; p++) {
    const o = p * 4;
    const key =
      (img.data[o] << 24) | (img.data[o + 1] << 16) | (img.data[o + 2] << 8) | img.data[o + 3];
    const label = lookup.get(key);
    if (label === undefined) throw new Error(`pixel ${p} color not in palette`);
    out[p] = label;
  }
  return out;
}

export function labelsToRgba(
  width: number,
  height: number,
  labels: ArrayLike<Label>,
  palette: readonly Rgba[],
): RgbaImage {
  const data = new Uint8Array(width * height * 4);
  for (let p = 0; p < width * height; p++) {
    const c = palette[labels[p]];
    if (!c) throw new Error(`no palette color for label ${labels[p]}`);
    data.set(c, p * 4);
  }
  return { width, height, data };
}

export function makeTestImage(): { img: RgbaImage; palette: Rgba[] } {
  const palette: Rgba[] = [
    [10, 20, 30, 255],
    [200, 30, 30, 255],
    [30, 200, 60, 255],
    [40, 80, 220, 255],
    [240, 220, 40, 255],
  ];
  const width = 13;
  const height = 11;
  const data = new Uint8Array(width * height * 4);
  const put = (x: number, y: number, ci: number) => data.set(palette[ci], (y * width + x) * 4);
  for (let y = 0; y < height; y++) for (let x = 0; x < width; x++) put(x, y, 0);
  for (let y = 1; y <= 4; y++) for (let x = 2; x <= 6; x++) put(x, y, 1);
  for (let y = 3; y <= 7; y++) for (let x = 5; x <= 9; x++) put(x, y, 2);
  for (let y = 6; y <= 9; y++) for (let x = 1; x <= 4; x++) put(x, y, 3);
  put(11, 2, 4);
  put(0, 10, 4);
  put(12, 10, 4);
  return { img: { width, height, data }, palette };
}
