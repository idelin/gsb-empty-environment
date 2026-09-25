export const CHANNELS = 4;

export type Rgba = readonly [number, number, number, number];

export function blankPixels(tileSize: number): Uint8Array {
  return new Uint8Array(tileSize * tileSize * CHANNELS);
}

export function setPx(
  buf: Uint8Array,
  tileSize: number,
  x: number,
  y: number,
  rgba: Rgba,
): void {
  const off = (y * tileSize + x) * CHANNELS;
  buf[off] = rgba[0];
  buf[off + 1] = rgba[1];
  buf[off + 2] = rgba[2];
  buf[off + 3] = rgba[3];
}

export function solidPixels(tileSize: number, rgba: Rgba): Uint8Array {
  const buf = blankPixels(tileSize);
  for (let y = 0; y < tileSize; y++) {
    for (let x = 0; x < tileSize; x++) setPx(buf, tileSize, x, y, rgba);
  }
  return buf;
}

export function checkerPixels(tileSize: number, a: Rgba, b: Rgba): Uint8Array {
  const buf = blankPixels(tileSize);
  for (let y = 0; y < tileSize; y++) {
    for (let x = 0; x < tileSize; x++) {
      setPx(buf, tileSize, x, y, (x + y) % 2 === 0 ? a : b);
    }
  }
  return buf;
}

export function gradientPixels(tileSize: number, seed: number): Uint8Array {
  const buf = blankPixels(tileSize);
  for (let y = 0; y < tileSize; y++) {
    for (let x = 0; x < tileSize; x++) {
      setPx(buf, tileSize, x, y, [
        (x * 16 + seed) & 0xff,
        (y * 16 + seed * 3) & 0xff,
        (x * y + seed * 7) & 0xff,
        255,
      ]);
    }
  }
  return buf;
}

export function pixelsEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}
