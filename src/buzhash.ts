import { splitmix32 } from './prng.ts';

const DEFAULT_SEED = 0x42424242;

function rotl32(x: number, r: number): number {
  r &= 31;
  return ((x << r) | (x >>> (32 - r))) >>> 0;
}

/**
 * Buzhash rolling hash over a sliding byte window.
 *
 * Definition for a window of bytes b[0..W-1]:
 *   h = XOR_i rotl32(T[b[i]], (W - 1 - i) mod 32)
 * where T is a deterministic table of 256 pseudo-random 32-bit values.
 *
 * Sliding one byte (out = b_out, in = b_in) is O(1):
 *   h' = rotl32(h, 1) ^ rotl32(T[b_out], W mod 32) ^ T[b_in]
 */
export class Buzhash {
  readonly windowSize: number;
  private readonly table: Uint32Array;
  private readonly window: Uint8Array;
  private pos = 0;
  private filled = 0;
  private hash = 0;

  constructor(windowSize = 64, seed = DEFAULT_SEED) {
    if (windowSize < 1) throw new Error('windowSize must be >= 1');
    this.windowSize = windowSize;
    const next = splitmix32(seed);
    this.table = new Uint32Array(256);
    for (let i = 0; i < 256; i++) this.table[i] = next();
    this.window = new Uint8Array(windowSize);
  }

  reset(): void {
    this.pos = 0;
    this.filled = 0;
    this.hash = 0;
  }

  /** Push one byte into the window; returns the current rolling hash. */
  update(byte: number): number {
    const out = this.filled < this.windowSize ? -1 : this.window[this.pos];
    if (out >= 0) {
      this.hash =
        (rotl32(this.hash, 1) ^
          rotl32(this.table[out], this.windowSize) ^
          this.table[byte]) >>>
        0;
    } else {
      this.hash = (rotl32(this.hash, 1) ^ this.table[byte]) >>> 0;
      this.filled++;
    }
    this.window[this.pos] = byte;
    this.pos = (this.pos + 1) % this.windowSize;
    return this.hash;
  }

  get digest(): number {
    return this.hash;
  }

  /** Hash a full window from scratch (ground truth for verifying the roller). */
  hashWindow(bytes: Uint8Array): number {
    if (bytes.length !== this.windowSize) {
      throw new Error(`expected ${this.windowSize} bytes, got ${bytes.length}`);
    }
    let h = 0;
    for (let i = 0; i < bytes.length; i++) {
      h = (h ^ rotl32(this.table[bytes[i]], this.windowSize - 1 - i)) >>> 0;
    }
    return h;
  }
}
