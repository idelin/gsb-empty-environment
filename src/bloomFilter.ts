/**
 * Bloom Filter：基于 FNV-1a 双哈希（double hashing）的概率型成员判定结构。
 * 只会误判（false positive），不会漏判（false negative）。
 */

function fnv1a32(input: string, seed: number): number {
  let hash = (0x811c9dc5 ^ seed) >>> 0;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

export class BloomFilter {
  private readonly bits: Uint8Array;
  readonly sizeBits: number;
  readonly numHashes: number;

  constructor(expectedItems: number, bitsPerKey = 10) {
    this.sizeBits = Math.max(64, Math.ceil(Math.max(1, expectedItems) * bitsPerKey));
    this.numHashes = Math.max(1, Math.round(bitsPerKey * Math.LN2));
    this.bits = new Uint8Array(Math.ceil(this.sizeBits / 8));
  }

  private positions(key: string): number[] {
    const h1 = fnv1a32(key, 0);
    const h2 = (fnv1a32(key, 0x9e3779b9) | 1) >>> 0;
    const out: number[] = new Array(this.numHashes);
    for (let i = 0; i < this.numHashes; i++) {
      out[i] = (h1 + i * h2) % this.sizeBits;
    }
    return out;
  }

  add(key: string): void {
    for (const pos of this.positions(key)) {
      this.bits[pos >> 3] |= 1 << (pos & 7);
    }
  }

  mightContain(key: string): boolean {
    for (const pos of this.positions(key)) {
      if ((this.bits[pos >> 3] & (1 << (pos & 7))) === 0) return false;
    }
    return true;
  }
}
