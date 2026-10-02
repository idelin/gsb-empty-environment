/**
 * Buzhash 滚动哈希（32 位，循环左移 1 位变体）。
 *
 * h = rotl1(h) XOR table[outByte] XOR tableWindow[inByte]
 *
 * 其中 tableWindow[b] = rotl_{window}(table[b])，即把字节移出窗口时的贡献。
 * 哈希表由确定性的 splitmix32 PRNG 生成，保证结果可验证、可复现。
 */

function splitmix32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x9e3779b9) >>> 0;
    let z = state;
    z = Math.imul(z ^ (z >>> 16), 0x21f0aaad);
    z = Math.imul(z ^ (z >>> 15), 0x735a2d97);
    return (z ^ (z >>> 15)) >>> 0;
  };
}

export const BUZHASH_SEED = 0x427a7a68; // "Bzzh"

function rotl32(x: number, r: number): number {
  r &= 31;
  return ((x << r) | (x >>> (32 - r))) >>> 0;
}

export class Buzhash {
  readonly windowSize: number;
  private readonly table: Uint32Array;
  private readonly tableOut: Uint32Array; // table[b] 旋转 windowSize 位后的值
  private window: Uint8Array;
  private pos = 0;
  private filled = 0;
  private hash = 0;

  constructor(windowSize = 48, seed = BUZHASH_SEED) {
    this.windowSize = windowSize;
    const rand = splitmix32(seed);
    this.table = new Uint32Array(256);
    for (let i = 0; i < 256; i++) this.table[i] = rand();
    this.tableOut = new Uint32Array(256);
    for (let i = 0; i < 256; i++) {
      this.tableOut[i] = rotl32(this.table[i], windowSize & 31);
    }
    this.window = new Uint8Array(windowSize);
  }

  reset(): void {
    this.window.fill(0);
    this.pos = 0;
    this.filled = 0;
    this.hash = 0;
  }

  /** 送入一个字节，返回当前窗口的滚动哈希值。 */
  update(byte: number): number {
    const evict = this.filled === this.windowSize;
    const out = this.window[this.pos];
    this.window[this.pos] = byte;
    this.pos = (this.pos + 1) % this.windowSize;
    if (this.filled < this.windowSize) this.filled++;
    let h = rotl32(this.hash, 1);
    if (evict) h ^= this.tableOut[out];
    h ^= this.table[byte];
    this.hash = h >>> 0;
    return this.hash;
  }

  get digest(): number {
    return this.hash;
  }
}
