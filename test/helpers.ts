import assert from 'node:assert/strict';
import { FloatImage } from '../src/index.ts';

export function lcg(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x100000000;
  };
}

export function makeFloat(
  w: number,
  h: number,
  fn: (x: number, y: number) => [number, number, number, number],
): FloatImage {
  const img = new FloatImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      img.set(x, y, fn(x, y));
    }
  }
  return img;
}

export function approx(actual: number, expected: number, eps: number, msg: string): void {
  assert.ok(
    Math.abs(actual - expected) <= eps,
    msg + ': expected ' + expected + ', got ' + actual,
  );
}

export function approxImage(a: FloatImage, b: FloatImage, eps: number, msg: string): void {
  assert.equal(a.width, b.width, msg + ': width');
  assert.equal(a.height, b.height, msg + ': height');
  for (let i = 0; i < a.data.length; i++) {
    assert.ok(
      Math.abs(a.data[i] - b.data[i]) <= eps,
      msg + ': data[' + i + '] expected ' + b.data[i] + ', got ' + a.data[i],
    );
  }
}
