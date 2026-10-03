/**
 * Classic 32-bit range (arithmetic) coder, used as the throughput baseline
 * that tANS is benchmarked against. Same probability model (normalised
 * frequencies summing to 2^tableLog) so the comparison is apples-to-apples.
 */
import { BitReader, BitWriter } from "./bitstream.ts";

const TOP = 0xffffffff;
const HALF = 0x80000000;
const Q1 = 0x40000000;
const Q3 = 0xc0000000;

export class ArithmeticEncoder {
  private low = 0;
  private high = TOP;
  private pending = 0;
  private readonly cum: Uint32Array;
  private readonly total: number;
  private readonly out: BitWriter;

  constructor(cum: Uint32Array, total: number, out: BitWriter) {
    this.cum = cum; // cumulative freqs, length = nsyms+1
    this.total = total;
    this.out = out;
  }

  encode(symbol: number): void {
    const range = this.high - this.low + 1;
    // 64-bit math avoids overflow; per-symbol multiply+divide here is what
    // makes arithmetic coding slower than a tANS table lookup.
    this.high =
      (this.low + Math.floor((range * this.cum[symbol + 1]) / this.total) - 1) >>> 0;
    this.low = (this.low + Math.floor((range * this.cum[symbol]) / this.total)) >>> 0;
    for (;;) {
      if ((this.low >>> 31) === (this.high >>> 31)) {
        this.emit(this.low >>> 31);
      } else if (this.low >= Q1 && this.high < Q3) {
        this.pending++;
        this.low = (this.low - Q1) >>> 0;
        this.high = (this.high - Q1) >>> 0;
      } else {
        break;
      }
      this.low = (this.low << 1) >>> 0;
      this.high = ((this.high << 1) | 1) >>> 0;
    }
  }

  private emit(bit: number): void {
    this.out.write(bit, 1);
    while (this.pending > 0) {
      this.out.write(bit ^ 1, 1);
      this.pending--;
    }
  }

  finish(): void {
    this.pending++;
    this.emit(this.low >= Q1 ? 1 : 0);
  }
}

export class ArithmeticDecoder {
  private low = 0;
  private high = TOP;
  private value = 0;
  private readonly cum: Uint32Array;
  private readonly total: number;
  private readonly input: BitReader;
  private readonly nsyms: number;

  constructor(cum: Uint32Array, total: number, input: BitReader, nsyms: number) {
    this.cum = cum;
    this.total = total;
    this.input = input;
    this.nsyms = nsyms;
    for (let i = 0; i < 32; i++) {
      this.value = ((this.value << 1) | this.readBit()) >>> 0;
    }
  }

  private bitPos = 0;
  private readBit(): number {
    if (this.bitPos >= this.input.remaining) return 0;
    this.bitPos++;
    return this.input.readForward(1);
  }

  decode(): number {
    const range = this.high - this.low + 1;
    const offset = this.value - this.low + 1;
    const scaled = Math.floor((offset * this.total - 1) / range);
    let s = 0;
    while (s + 1 < this.cum.length && this.cum[s + 1] <= scaled) s++;
    if (s >= this.nsyms) s = this.nsyms - 1;
    this.high =
      (this.low + Math.floor((range * this.cum[s + 1]) / this.total) - 1) >>> 0;
    this.low = (this.low + Math.floor((range * this.cum[s]) / this.total)) >>> 0;
    for (;;) {
      if ((this.low >>> 31) === (this.high >>> 31)) {
        // nothing
      } else if (this.low >= Q1 && this.high < Q3) {
        this.value = (this.value - Q1) >>> 0;
        this.low = (this.low - Q1) >>> 0;
        this.high = (this.high - Q1) >>> 0;
      } else {
        break;
      }
      this.low = (this.low << 1) >>> 0;
      this.high = ((this.high << 1) | 1) >>> 0;
      this.value = ((this.value << 1) | this.readBit()) >>> 0;
    }
    return s;
  }
}

/** Build cumulative frequencies from a normalised freq table. */
export function cumulative(freq: Uint32Array): Uint32Array {
  const cum = new Uint32Array(freq.length + 1);
  for (let i = 0; i < freq.length; i++) cum[i + 1] = cum[i] + freq[i];
  return cum;
}
