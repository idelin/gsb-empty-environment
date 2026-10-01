import { BitWriter, BitReader } from "./bitstream.js";

// Fixed-point precision of the interval state, in bits. The state values are
// numerators over the shared denominator 2^PRECISION, i.e. the current
// interval is the rational range [low / 2^P, high / 2^P). Using BigInt keeps
// the precision far beyond classic 32/64-bit implementations, so the emitted
// code length is indistinguishable from the Shannon entropy limit.
export const PRECISION: number = 512;

const FULL: bigint = 1n << BigInt(PRECISION);
const HALF: bigint = FULL >> 1n;
const QUARTER: bigint = FULL >> 2n;
const THREE_QUARTER: bigint = 3n * QUARTER;

export class ArithmeticEncoder {
  private low: bigint = 0n;
  private high: bigint = FULL;
  private pending: number = 0;
  private writer: BitWriter;

  constructor(writer: BitWriter) {
    this.writer = writer;
  }

  // Narrow [low, high) to the sub-interval [cumLow/total, cumHigh/total)
  // according to the probability model, then renormalize.
  encode(cumLow: number, cumHigh: number, total: number): void {
    const range: bigint = this.high - this.low;
    const t: bigint = BigInt(total);
    this.high = this.low + (range * BigInt(cumHigh)) / t;
    this.low = this.low + (range * BigInt(cumLow)) / t;
    this.renormalize();
  }

  private emit(bit: number): void {
    this.writer.writeBit(bit);
    const follow: number = bit ^ 1;
    while (this.pending > 0) {
      this.writer.writeBit(follow);
      this.pending -= 1;
    }
  }

  // E1/E2/E3 renormalization: while the interval lies fully in the lower
  // half, the upper half, or the middle half, emit (or defer) one bit and
  // rescale the interval back to [0, 1).
  private renormalize(): void {
    for (;;) {
      if (this.high <= HALF) {
        this.emit(0);
      } else if (this.low >= HALF) {
        this.emit(1);
        this.low -= HALF;
        this.high -= HALF;
      } else if (this.low >= QUARTER && this.high <= THREE_QUARTER) {
        this.pending += 1;
        this.low -= QUARTER;
        this.high -= QUARTER;
      } else {
        return;
      }
      this.low <<= 1n;
      this.high <<= 1n;
    }
  }

  // Flush the final state: two bits suffice to disambiguate the interval.
  finish(): void {
    this.pending += 1;
    if (this.low < QUARTER) {
      this.emit(0);
    } else {
      this.emit(1);
    }
  }
}

export class ArithmeticDecoder {
  private low: bigint = 0n;
  private high: bigint = FULL;
  private code: bigint = 0n;
  private reader: BitReader;

  constructor(reader: BitReader) {
    this.reader = reader;
    for (let i: number = 0; i < PRECISION; i++) {
      this.code = (this.code << 1n) | BigInt(this.reader.readBit());
    }
  }

  // Project the code value onto the cumulative-frequency axis: returns the
  // scaled value v in [0, total) used to look up the next symbol.
  scale(total: number): number {
    const range: bigint = this.high - this.low;
    const scaled: bigint = ((this.code - this.low + 1n) * BigInt(total) - 1n) / range;
    return Number(scaled);
  }

  // Apply the exact same interval subdivision and renormalization as the
  // encoder, shifting fresh bits into the code value.
  update(cumLow: number, cumHigh: number, total: number): void {
    const range: bigint = this.high - this.low;
    const t: bigint = BigInt(total);
    this.high = this.low + (range * BigInt(cumHigh)) / t;
    this.low = this.low + (range * BigInt(cumLow)) / t;
    for (;;) {
      if (this.high <= HALF) {
        // Interval in lower half: nothing to subtract.
      } else if (this.low >= HALF) {
        this.low -= HALF;
        this.high -= HALF;
        this.code -= HALF;
      } else if (this.low >= QUARTER && this.high <= THREE_QUARTER) {
        this.low -= QUARTER;
        this.high -= QUARTER;
        this.code -= QUARTER;
      } else {
        return;
      }
      this.low <<= 1n;
      this.high <<= 1n;
      this.code = (this.code << 1n) | BigInt(this.reader.readBit());
    }
  }
}
