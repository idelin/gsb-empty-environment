/** Binary foreground/background mask. data[y * width + x] is 1 (foreground) or 0. */
export interface Mask {
  width: number;
  height: number;
  data: Uint8Array;
}

export function createMask(width: number, height: number): Mask {
  return { width, height, data: new Uint8Array(width * height) };
}

/**
 * Convert an RGBA byte buffer (4 bytes per pixel, row-major) into a binary mask.
 * A pixel is foreground when it is painted (alpha > 0) and bright
 * (Rec.601 luma >= luminanceThreshold). No canvas / image libraries involved.
 */
export function maskFromRgba(
  rgba: Uint8ClampedArray | Uint8Array | number[],
  width: number,
  height: number,
  luminanceThreshold = 128,
): Mask {
  if (rgba.length !== width * height * 4) {
    throw new RangeError(
      `expected ${width * height * 4} RGBA bytes, got ${rgba.length}`,
    );
  }
  const mask = createMask(width, height);
  for (let i = 0; i < width * height; i++) {
    const o = i * 4;
    const alpha = rgba[o + 3];
    const luma = (rgba[o] * 299 + rgba[o + 1] * 587 + rgba[o + 2] * 114) / 1000;
    mask.data[i] = alpha > 0 && luma >= luminanceThreshold ? 1 : 0;
  }
  return mask;
}

export function masksEqual(a: Mask, b: Mask): boolean {
  if (a.width !== b.width || a.height !== b.height) return false;
  for (let i = 0; i < a.data.length; i++) {
    if ((a.data[i] ? 1 : 0) !== (b.data[i] ? 1 : 0)) return false;
  }
  return true;
}

/** Render the mask as ASCII art ('#' foreground, '.' background). */
export function maskToAscii(mask: Mask): string {
  const rows: string[] = [];
  for (let y = 0; y < mask.height; y++) {
    let row = "";
    for (let x = 0; x < mask.width; x++) {
      row += mask.data[y * mask.width + x] ? "#" : ".";
    }
    rows.push(row);
  }
  return rows.join("\n");
}
