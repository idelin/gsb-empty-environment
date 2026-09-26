import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { CHANNELS } from '../src/metrics.ts';
import { makeRaster, type Raster } from '../src/reference.ts';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Deterministic RGBA test image with values in [0,1). */
export function randomRaster(width: number, height: number, seed = 42): Raster {
  const rand = mulberry32(seed);
  const data = new Float32Array(width * height * CHANNELS);
  for (let i = 0; i < data.length; i++) data[i] = rand();
  return makeRaster(width, height, data);
}

export function assertRasterExact(actual: Raster, expected: Raster, label: string): void {
  if (actual.width !== expected.width || actual.height !== expected.height) {
    throw new Error(`${label}: dimension mismatch`);
  }
  for (let i = 0; i < actual.data.length; i++) {
    if (actual.data[i] !== expected.data[i]) {
      const px = Math.floor(i / CHANNELS);
      throw new Error(
        `${label}: mismatch at pixel (${px % actual.width},${Math.floor(px / actual.width)}) ` +
          `channel ${i % CHANNELS}: ${actual.data[i]} !== ${expected.data[i]}`,
      );
    }
  }
}

export function tempWorkDir(prefix: string): string {
  return mkdtempSync(join(tmpdir(), prefix));
}

export function cleanupDir(dir: string): void {
  rmSync(dir, { recursive: true, force: true });
}

/** Generous budget that never constrains a run. */
export const UNLIMITED_BUDGET = 1 << 30;
