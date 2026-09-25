export interface RgbaImage {
  width: number;
  height: number;
  data: Uint8ClampedArray;
}

export type RgbaColor = [number, number, number, number];

const DEMO_PALETTE: RgbaColor[] = [
  [255, 0, 0, 255],
  [0, 192, 0, 255],
  [0, 0, 255, 255],
  [255, 255, 0, 255],
  [192, 0, 192, 255],
  [0, 192, 192, 255],
];

export function makeTestImage(width: number, height: number): RgbaImage {
  const data = new Uint8ClampedArray(width * height * 4);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      let idx: number;
      if (x >= (width * 2) / 3 && y < height / 3) idx = 4;
      else if (x < width / 3 && y >= (height * 2) / 3) idx = 5;
      else if ((x + y) % 6 < 2) idx = 1;
      else if ((Math.floor(x / 4) + Math.floor(y / 3)) % 3 === 0) idx = 0;
      else if ((x * y) % 5 === 0) idx = 3;
      else idx = 2;
      const c = DEMO_PALETTE[idx];
      const o = (y * width + x) * 4;
      data[o] = c[0];
      data[o + 1] = c[1];
      data[o + 2] = c[2];
      data[o + 3] = c[3];
    }
  }
  return { width, height, data };
}

export function colorKey(r: number, g: number, b: number, a: number): number {
  return (((r << 24) | (g << 16) | (b << 8) | a) >>> 0);
}

export function buildPalette(img: RgbaImage): RgbaColor[] {
  const seen = new Map<number, RgbaColor>();
  for (let i = 0; i < img.data.length; i += 4) {
    const r = img.data[i];
    const g = img.data[i + 1];
    const b = img.data[i + 2];
    const a = img.data[i + 3];
    const key = colorKey(r, g, b, a);
    if (!seen.has(key)) seen.set(key, [r, g, b, a]);
  }
  return [...seen.values()];
}

export function imageToLabels(img: RgbaImage, palette: RgbaColor[]): number[] {
  const lookup = new Map<number, number>();
  palette.forEach((c, i) => lookup.set(colorKey(c[0], c[1], c[2], c[3]), i));
  const labels = new Array<number>(img.width * img.height);
  for (let p = 0; p < labels.length; p++) {
    const o = p * 4;
    const key = colorKey(img.data[o], img.data[o + 1], img.data[o + 2], img.data[o + 3]);
    const label = lookup.get(key);
    if (label === undefined) {
      throw new Error(`pixel ${p}: color #${key.toString(16)} not in palette`);
    }
    labels[p] = label;
  }
  return labels;
}

export function labelsToImage(
  labels: ArrayLike<number>,
  width: number,
  height: number,
  palette: RgbaColor[],
): RgbaImage {
  if (labels.length !== width * height) {
    throw new Error(`expected ${width * height} labels, got ${labels.length}`);
  }
  const data = new Uint8ClampedArray(width * height * 4);
  for (let p = 0; p < labels.length; p++) {
    const c = palette[labels[p]];
    if (!c) throw new Error(`unknown label ${labels[p]} at pixel ${p}`);
    const o = p * 4;
    data[o] = c[0];
    data[o + 1] = c[1];
    data[o + 2] = c[2];
    data[o + 3] = c[3];
  }
  return { width, height, data };
}

export interface ImageDiff {
  equal: boolean;
  diffCount: number;
  firstDiffs: Array<{ x: number; y: number; a: string; b: string }>;
}

export function compareImages(a: RgbaImage, b: RgbaImage): ImageDiff {
  if (a.width !== b.width || a.height !== b.height) {
    throw new Error(`size mismatch: ${a.width}x${a.height} vs ${b.width}x${b.height}`);
  }
  const firstDiffs: ImageDiff["firstDiffs"] = [];
  let diffCount = 0;
  for (let p = 0; p < a.width * a.height; p++) {
    const o = p * 4;
    const same =
      a.data[o] === b.data[o] &&
      a.data[o + 1] === b.data[o + 1] &&
      a.data[o + 2] === b.data[o + 2] &&
      a.data[o + 3] === b.data[o + 3];
    if (!same) {
      diffCount++;
      if (firstDiffs.length < 8) {
        const px = (img: RgbaImage): string =>
          `rgba(${img.data[o]},${img.data[o + 1]},${img.data[o + 2]},${img.data[o + 3]})`;
        firstDiffs.push({
          x: p % a.width,
          y: Math.floor(p / a.width),
          a: px(a),
          b: px(b),
        });
      }
    }
  }
  return { equal: diffCount === 0, diffCount, firstDiffs };
}
