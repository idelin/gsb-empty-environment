import type { AABB } from "../src/aabb.ts";
import { overlaps } from "../src/aabb.ts";

/** 确定性伪随机数发生器（mulberry32）。 */
export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function randomAabb(rand: () => number, extent: number, maxSize: number): AABB {
  const cx = (rand() * 2 - 1) * extent;
  const cy = (rand() * 2 - 1) * extent;
  const cz = (rand() * 2 - 1) * extent;
  const hx = 0.1 + rand() * maxSize;
  const hy = 0.1 + rand() * maxSize;
  const hz = 0.1 + rand() * maxSize;
  return {
    minX: cx - hx, minY: cy - hy, minZ: cz - hz,
    maxX: cx + hx, maxY: cy + hy, maxZ: cz + hz,
  };
}

/** O(N^2) 暴力比对，作为测试基准。 */
export function bruteForcePairs<T>(items: Array<[T, AABB]>): Array<[T, T]> {
  const pairs: Array<[T, T]> = [];
  for (let i = 0; i < items.length; i++) {
    for (let j = i + 1; j < items.length; j++) {
      if (overlaps(items[i][1], items[j][1])) {
        pairs.push([items[i][0], items[j][0]]);
      }
    }
  }
  return pairs;
}

/** 将实体对列表规范化为可比较的字符串集合。 */
export function pairKeys<T>(pairs: Array<[T, T]>): Set<string> {
  const keys = new Set<string>();
  for (const [a, b] of pairs) {
    const sa = String(a);
    const sb = String(b);
    keys.add(sa < sb ? `${sa}|${sb}` : `${sb}|${sa}`);
  }
  return keys;
}
