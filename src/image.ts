import { Mask, clamp01 } from "./mask.ts";

/** Straight-alpha RGBA image backed by a Uint8ClampedArray. */
export class RGBAImage {
  readonly width: number;
  readonly height: number;
  readonly data: Uint8ClampedArray;

  constructor(width: number, height: number, data?: Uint8ClampedArray) {
    this.width = width;
    this.height = height;
    this.data = data ?? new Uint8ClampedArray(width * height * 4);
  }

  static filled(
    width: number,
    height: number,
    color: readonly [number, number, number, number],
  ): RGBAImage {
    const img = new RGBAImage(width, height);
    for (let i = 0; i < img.data.length; i += 4) {
      img.data[i] = color[0];
      img.data[i + 1] = color[1];
      img.data[i + 2] = color[2];
      img.data[i + 3] = color[3];
    }
    return img;
  }

  pixel(x: number, y: number): [number, number, number, number] {
    const i = (y * this.width + x) * 4;
    return [this.data[i], this.data[i + 1], this.data[i + 2], this.data[i + 3]];
  }
}

/** Paints a solid color through a coverage mask using source-over compositing. */
export function paint(
  img: RGBAImage,
  mask: Mask,
  color: readonly [number, number, number, number],
): void {
  const sa = color[3] / 255;
  for (let i = 0, p = 0; i < mask.data.length; i++, p += 4) {
    const a = clamp01(mask.data[i] * sa);
    if (a === 0) continue;
    const inv = 1 - a;
    img.data[p] = color[0] * a + img.data[p] * inv;
    img.data[p + 1] = color[1] * a + img.data[p + 1] * inv;
    img.data[p + 2] = color[2] * a + img.data[p + 2] * inv;
    img.data[p + 3] = 255 * a + img.data[p + 3] * inv;
  }
}

/**
 * Applies a mask to the image's alpha channel: alpha *= coverage.
 * Repeated applications multiply repeatedly (per the math definition);
 * no idempotence clamping is applied.
 */
export function applyMask(img: RGBAImage, mask: Mask): void {
  for (let i = 0, p = 3; i < mask.data.length; i++, p += 4) {
    img.data[p] = img.data[p] * clamp01(mask.data[i]);
  }
}

const RAMP = " .:-=+*#%@";

/** ASCII preview of a mask (10 levels). */
export function maskToAscii(mask: Mask): string {
  const lines: string[] = [];
  for (let y = 0; y < mask.height; y++) {
    let line = "";
    for (let x = 0; x < mask.width; x++) {
      const v = clamp01(mask.at(x, y));
      line += RAMP[Math.min(RAMP.length - 1, Math.floor(v * RAMP.length))];
    }
    lines.push(line);
  }
  return lines.join("\n");
}

/** Binary PGM (P5) of a mask, for visual inspection with external tools. */
export function maskToPgm(mask: Mask): Buffer {
  const header = Buffer.from(`P5\n${mask.width} ${mask.height}\n255\n`, "ascii");
  const body = Buffer.alloc(mask.width * mask.height);
  for (let i = 0; i < mask.data.length; i++) {
    body[i] = Math.round(clamp01(mask.data[i]) * 255);
  }
  return Buffer.concat([header, body]);
}

/** Binary PPM (P6); transparent pixels are composited over white. */
export function imageToPpm(img: RGBAImage): Buffer {
  const header = Buffer.from(`P6\n${img.width} ${img.height}\n255\n`, "ascii");
  const body = Buffer.alloc(img.width * img.height * 3);
  for (let i = 0, p = 0; i < img.width * img.height; i++, p += 4) {
    const a = img.data[p + 3] / 255;
    body[i * 3] = img.data[p] * a + 255 * (1 - a);
    body[i * 3 + 1] = img.data[p + 1] * a + 255 * (1 - a);
    body[i * 3 + 2] = img.data[p + 2] * a + 255 * (1 - a);
  }
  return Buffer.concat([header, body]);
}
