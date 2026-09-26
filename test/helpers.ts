import { CHANNELS } from '../src/types.ts';

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

// 自建 RGBA float32 测试图 (确定性伪随机)
export function makeImage(w: number, h: number, seed = 42): Float32Array {
  const rnd = mulberry32(seed);
  const img = new Float32Array(w * h * CHANNELS);
  for (let i = 0; i < img.length; i++) img[i] = rnd();
  return img;
}

export function maxDiff(a: Float32Array, b: Float32Array): number {
  let max = 0;
  for (let i = 0; i < a.length; i++) max = Math.max(max, Math.abs(a[i] - b[i]));
  return max;
}
