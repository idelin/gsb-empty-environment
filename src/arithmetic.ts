import { BitWriter, BitReader } from './bitstream.ts';
import { AdaptiveBinaryModel } from './model.ts';

/**
 * Arithmetic coder for a binary symbol stream.
 *
 * The current state interval [low, high) is maintained exactly with big
 * integers over a fixed 63-bit code space. Each symbol subdivides the
 * interval proportionally to the model's cumulative probability
 * distribution (exact rational arithmetic: low + range * cum / total).
 * Renormalization (E1/E2/E3 with underflow scaling) shifts bits out to the
 * bit stream, so the compressed size approaches the Shannon entropy limit.
 */

const CODE_BITS = 63n;
const TOP = 1n << CODE_BITS; // size of the code space: [0, TOP)
const HALF = TOP >> 1n;
const FIRST_QTR = HALF >> 1n;
const THIRD_QTR = FIRST_QTR * 3n;

class ArithmeticEncoder {
  private low = 0n;
  private high = TOP;
  private pendingBits = 0;
  private readonly writer = new BitWriter();
  private readonly model = new AdaptiveBinaryModel();

  encodeSymbol(symbol: 0 | 1): void {
    const { cumLow, cumHigh, total } = this.model.cumulative(symbol);
    const range = this.high - this.low;
    // Exact rational subdivision of [low, high).
    const newHigh = this.low + (range * cumHigh) / total;
    const newLow = this.low + (range * cumLow) / total;
    this.high = newHigh;
    this.low = newLow;
    this.renormalize();
    this.model.update(symbol);
  }

  private renormalize(): void {
    for (;;) {
      if (this.high <= HALF) {
        this.emit(0); // E1: interval entirely in the lower half
      } else if (this.low >= HALF) {
        this.emit(1); // E2: interval entirely in the upper half
        this.low -= HALF;
        this.high -= HALF;
      } else if (this.low >= FIRST_QTR && this.high <= THIRD_QTR) {
        // E3: interval straddles the midpoint; defer the decision.
        this.pendingBits++;
        this.low -= FIRST_QTR;
        this.high -= FIRST_QTR;
      } else {
        break;
      }
      this.low <<= 1n;
      this.high <<= 1n;
    }
  }

  private emit(bit: 0 | 1): void {
    this.writer.writeBit(bit);
    const follow = (bit ^ 1) as 0 | 1;
    while (this.pendingBits > 0) {
      this.writer.writeBit(follow);
      this.pendingBits--;
    }
  }

  finish(): Buffer {
    // Disambiguate the final interval: low now lies in [FIRST_QTR, THIRD_QTR).
    this.pendingBits++;
    this.emit(this.low < FIRST_QTR ? 0 : 1);
    return this.writer.finish();
  }
}

class ArithmeticDecoder {
  private low = 0n;
  private high = TOP;
  private value = 0n;
  private readonly reader: BitReader;
  private readonly model = new AdaptiveBinaryModel();

  constructor(data: Buffer) {
    this.reader = new BitReader(data);
    for (let i = 0n; i < CODE_BITS; i++) {
      this.value = (this.value << 1n) | BigInt(this.reader.readBit());
    }
  }

  decodeSymbol(): 0 | 1 {
    const total = this.model.cumulative(0).total;
    const range = this.high - this.low;
    // Invert the encoder's rational subdivision.
    const cum = ((this.value - this.low) * total) / range;
    const symbol = this.model.symbolForCumulative(cum);
    const { cumLow, cumHigh } = this.model.cumulative(symbol);
    const newHigh = this.low + (range * cumHigh) / total;
    const newLow = this.low + (range * cumLow) / total;
    this.high = newHigh;
    this.low = newLow;
    this.renormalize();
    this.model.update(symbol);
    return symbol;
  }

  private renormalize(): void {
    for (;;) {
      if (this.high <= HALF) {
        // nothing to subtract
      } else if (this.low >= HALF) {
        this.value -= HALF;
        this.low -= HALF;
        this.high -= HALF;
      } else if (this.low >= FIRST_QTR && this.high <= THIRD_QTR) {
        this.value -= FIRST_QTR;
        this.low -= FIRST_QTR;
        this.high -= FIRST_QTR;
      } else {
        break;
      }
      this.low <<= 1n;
      this.high <<= 1n;
      this.value = (this.value << 1n) | BigInt(this.reader.readBit());
    }
  }
}

/** Encode the symbol count as an unsigned LEB128 varint header. */
function writeVarint(n: number): number[] {
  const bytes: number[] = [];
  let value = n;
  do {
    let byte = value & 0x7f;
    value = Math.floor(value / 128);
    if (value > 0) byte |= 0x80;
    bytes.push(byte);
  } while (value > 0);
  return bytes;
}

function readVarint(data: Buffer, offset: number): { value: number; offset: number } {
  let value = 0;
  let shift = 0;
  let i = offset;
  for (;;) {
    const byte = data[i++];
    value += (byte & 0x7f) * 2 ** shift;
    if ((byte & 0x80) === 0) break;
    shift += 7;
  }
  return { value, offset: i };
}

/**
 * Compress a stream of 0/1 symbols.
 * Output layout: varint(symbolCount) || arithmetic-coded bitstream bytes.
 */
export function encode(symbols: ArrayLike<number>): Buffer {
  const encoder = new ArithmeticEncoder();
  for (let i = 0; i < symbols.length; i++) {
    encoder.encodeSymbol(symbols[i] === 0 ? 0 : 1);
  }
  const body = encoder.finish();
  return Buffer.concat([Buffer.from(writeVarint(symbols.length)), body]);
}

/** Decompress data produced by {@link encode}; terminates after exactly the encoded symbol count. */
export function decode(data: Buffer): Uint8Array {
  const { value: count, offset } = readVarint(data, 0);
  const decoder = new ArithmeticDecoder(data.subarray(offset));
  const out = new Uint8Array(count);
  for (let i = 0; i < count; i++) {
    out[i] = decoder.decodeSymbol();
  }
  return out;
}

/** Shannon entropy (bits per symbol) of a binary source with P(0) = p0. */
export function binaryEntropy(p0: number): number {
  if (p0 <= 0 || p0 >= 1) return 0;
  const p1 = 1 - p0;
  return -(p0 * Math.log2(p0) + p1 * Math.log2(p1));
}
