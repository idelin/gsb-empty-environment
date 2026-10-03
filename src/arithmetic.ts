import { ALPHABET_SIZE } from "./normalize.ts";

/**
 * Classic 32-bit range/arithmetic coder (Witten-Neal-Cleary style) with a
 * static distribution, used as the throughput/ratio baseline that tANS is
 * compared against. It does bit-by-bit renormalization with underflow
 * handling — correct, but inherently serial per bit, unlike table-driven tANS.
 */

const TOP = 4294967295; // 2^32 - 1
const HALF = 2147483648; // 2^31
const Q1 = 1073741824; // 2^30
const Q3 = 3221225472; // 3 * 2^30

export function buildCumulative(norm: Uint32Array): Uint32Array {
  const cumul = new Uint32Array(ALPHABET_SIZE + 1);
  for (let s = 0; s < ALPHABET_SIZE; s++) cumul[s + 1] = cumul[s] + norm[s];
  return cumul;
}

class ForwardBitWriter {
  bytes: number[] = [];
  acc = 0;
  nbits = 0;

  bit(b: number): void {
    this.acc = this.acc * 2 + b;
    this.nbits++;
    if (this.nbits === 8) {
      this.bytes.push(this.acc);
      this.acc = 0;
      this.nbits = 0;
    }
  }

  finish(): Uint8Array {
    if (this.nbits > 0) this.bytes.push(this.acc * 2 ** (8 - this.nbits));
    return Uint8Array.from(this.bytes);
  }
}

class ForwardBitReader {
  pos = 0;
  bytes: Uint8Array;
  constructor(bytes: Uint8Array) {
    this.bytes = bytes;
  }

  bit(): number {
    const i = this.pos++;
    const b = this.bytes[i >> 3];
    if (b === undefined) return 0; // implicit zero tail
    return (b >> (7 - (i & 7))) & 1;
  }
}

export class ArithmeticEncoder {
  low = 0;
  high = TOP;
  pending = 0;
  private out = new ForwardBitWriter();

  encodeSymbol(cumul: Uint32Array, symbol: number, total: number): void {
    const range = Math.floor((this.high - this.low + 1) / total);
    this.high = this.low + range * cumul[symbol + 1] - 1;
    this.low = this.low + range * cumul[symbol];
    for (;;) {
      if (this.high < HALF) {
        this.emit(0);
      } else if (this.low >= HALF) {
        this.emit(1);
        this.low -= HALF;
        this.high -= HALF;
      } else if (this.low >= Q1 && this.high < Q3) {
        this.pending++;
        this.low -= Q1;
        this.high -= Q1;
      } else {
        break;
      }
      this.low *= 2;
      this.high = this.high * 2 + 1;
    }
  }

  private emit(bit: number): void {
    this.out.bit(bit);
    while (this.pending > 0) {
      this.out.bit(1 - bit);
      this.pending--;
    }
  }

  finish(): Uint8Array {
    this.pending++;
    if (this.low >= Q1) this.emit(1);
    else this.emit(0);
    return this.out.finish();
  }
}

export class ArithmeticDecoder {
  low = 0;
  high = TOP;
  value = 0;
  private reader: ForwardBitReader;

  constructor(bytes: Uint8Array) {
    this.reader = new ForwardBitReader(bytes);
    for (let i = 0; i < 32; i++) this.value = this.value * 2 + this.reader.bit();
  }

  /** Cumulative-frequency target for the current window. */
  target(total: number): number {
    const range = Math.floor((this.high - this.low + 1) / total);
    const t = Math.floor((this.value - this.low) / range);
    return t >= total ? total - 1 : t;
  }

  update(cumul: Uint32Array, symbol: number, total: number): void {
    const range = Math.floor((this.high - this.low + 1) / total);
    this.high = this.low + range * cumul[symbol + 1] - 1;
    this.low = this.low + range * cumul[symbol];
    for (;;) {
      if (this.high < HALF) {
        // nothing
      } else if (this.low >= HALF) {
        this.low -= HALF;
        this.high -= HALF;
        this.value -= HALF;
      } else if (this.low >= Q1 && this.high < Q3) {
        this.low -= Q1;
        this.high -= Q1;
        this.value -= Q1;
      } else {
        break;
      }
      this.low *= 2;
      this.high = this.high * 2 + 1;
      this.value = this.value * 2 + this.reader.bit();
    }
  }
}

/** Encode `data` with the given static distribution; returns raw bitstream. */
export function arithmeticCompress(
  data: Uint8Array,
  norm: Uint32Array,
  tableLog: number,
): Uint8Array {
  const cumul = buildCumulative(norm);
  const total = 1 << tableLog;
  const enc = new ArithmeticEncoder();
  for (let i = 0; i < data.length; i++) enc.encodeSymbol(cumul, data[i], total);
  return enc.finish();
}

export function arithmeticDecompress(
  bytes: Uint8Array,
  norm: Uint32Array,
  tableLog: number,
  length: number,
): Uint8Array {
  const cumul = buildCumulative(norm);
  const total = 1 << tableLog;
  const dec = new ArithmeticDecoder(bytes);
  const out = new Uint8Array(length);
  for (let i = 0; i < length; i++) {
    const t = dec.target(total);
    // Binary search for cumul[s] <= t < cumul[s+1].
    let lo = 0;
    let hi = ALPHABET_SIZE - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (cumul[mid] <= t) lo = mid;
      else hi = mid - 1;
    }
    out[i] = lo;
    dec.update(cumul, lo, total);
  }
  return out;
}
