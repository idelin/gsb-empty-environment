/**
 * Bloom Filter：用于 SSTable 的成员快速判定。
 * 采用 FNV-1a 双哈希（double hashing）生成 k 个哈希位。
 */

/** murmur3 风格 32 位终结混合，打散低位相关性 */
function fmix32(hash: number): number {
  hash ^= hash >>> 16;
  hash = Math.imul(hash, 0x85ebca6b) >>> 0;
  hash ^= hash >>> 13;
  hash = Math.imul(hash, 0xc2b2ae35) >>> 0;
  hash ^= hash >>> 16;
  return hash >>> 0;
}

export function fnv1a(input: string, seed = 0x811c9dc5): number {
  let hash = seed >>> 0;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return fmix32(hash ^ (hash << 5));
}

export class BloomFilter {
  readonly bitCount: number;
  readonly hashCount: number;
  private readonly bits: Uint8Array;
  private added = 0;

  /** expectedItems: 预期元素数；fpRate: 目标误判率（如 0.01） */
  constructor(expectedItems: number, fpRate = 0.01) {
    const n = Math.max(1, expectedItems);
    const m = Math.ceil((-n * Math.log(fpRate)) / (Math.LN2 * Math.LN2));
    this.bitCount = Math.max(64, m);
    this.hashCount = Math.max(1, Math.round((this.bitCount / n) * Math.LN2));
    this.bits = new Uint8Array(Math.ceil(this.bitCount / 8));
  }

  private positions(key: string): number[] {
    const h1 = fnv1a(key);
    const h2 = fnv1a(key, 0xdeadbeef) || 0x9e3779b9;
    const out: number[] = [];
    for (let i = 0; i < this.hashCount; i++) {
      out.push((h1 + i * h2) % this.bitCount);
    }
    return out;
  }

  add(key: string): void {
    for (const pos of this.positions(key)) {
      this.bits[pos >> 3] |= 1 << (pos & 7);
    }
    this.added++;
  }

  mightContain(key: string): boolean {
    for (const pos of this.positions(key)) {
      if ((this.bits[pos >> 3] & (1 << (pos & 7))) === 0) return false;
    }
    return true;
  }

  get size(): number {
    return this.added;
  }
}
