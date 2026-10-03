/**
 * hash.ts — 快速双通道 32 位哈希（cyrb64 风格）。
 *
 * 每行数据被映射为一个 64 位逻辑哈希（两个 32 位通道），
 * 用于滚动 XOR 校验和。生产环境可替换为 SHA-256 截断，
 * 接口保持不变。
 */

export type Hash64 = readonly [number, number];

export const ZERO_HASH: Hash64 = [0, 0];

/** cyrb64：单次遍历产出两个 32 位无符号哈希通道。 */
export function hashString(input: string, seed = 0): Hash64 {
  let h1 = 0xdeadbeef ^ seed;
  let h2 = 0x41c6ce57 ^ seed;
  for (let i = 0; i < input.length; i++) {
    const ch = input.charCodeAt(i);
    h1 = Math.imul(h1 ^ ch, 2654435761);
    h2 = Math.imul(h2 ^ ch, 1597334677);
  }
  h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507);
  h1 ^= Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507);
  h2 ^= Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return [h2 >>> 0, h1 >>> 0];
}

export function xorHash(a: Hash64, b: Hash64): Hash64 {
  return [(a[0] ^ b[0]) >>> 0, (a[1] ^ b[1]) >>> 0];
}

export function hashEquals(a: Hash64, b: Hash64): boolean {
  return a[0] === b[0] && a[1] === b[1];
}

export function isZeroHash(h: Hash64): boolean {
  return h[0] === 0 && h[1] === 0;
}

export function hashToHex(h: Hash64): string {
  return h[0].toString(16).padStart(8, "0") + h[1].toString(16).padStart(8, "0");
}
