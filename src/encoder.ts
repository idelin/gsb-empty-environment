import { BitWriter } from "./bitio.ts";
import type { Bin, ContextModel } from "./context.ts";
import { RANGE_TAB_LPS } from "./tables.ts";

/**
 * CABAC binary arithmetic encoder (H.264-style M-coder).
 *
 * Maintains a 9-bit interval `range` and a 10-bit `low` offset. Carry
 * propagation is handled with the standard "bit plus outstanding bits"
 * scheme, so output is produced incrementally with O(1) memory.
 */
export class CabacEncoder {
  private range = 510;
  private low = 0;
  private bitsOutstanding = 0;
  private readonly writer = new BitWriter();

  /** Bits emitted so far (including pending outstanding bits). */
  get bitsEmitted(): number {
    return this.writer.totalBits + this.bitsOutstanding;
  }

  /** Encode one bin with an adaptive regular context. */
  encodeBin(ctx: ContextModel, bin: Bin): void {
    const rLps = RANGE_TAB_LPS[ctx.stateIdx][(this.range >> 6) & 3];
    this.range -= rLps;
    if (bin === ctx.valMPS) {
      ctx.updateMPS();
    } else {
      this.low += this.range;
      this.range = rLps;
      ctx.updateLPS();
    }
    this.renormalize();
  }

  /** Encode one bin in bypass mode (fixed 50/50 probability, no context). */
  encodeBypass(bin: Bin): void {
    this.low <<= 1;
    if (bin === 1) this.low += this.range;
    if (this.low >= 1024) {
      this.putBit(1);
      this.low -= 1024;
    } else if (this.low < 512) {
      this.putBit(0);
    } else {
      this.low -= 512;
      this.bitsOutstanding++;
    }
  }

  /** Flush the encoder and return the byte stream. */
  finish(): Uint8Array {
    this.bitsOutstanding++;
    this.putBit(this.low < 256 ? 0 : 1);
    this.writer.alignToByte();
    return this.writer.toBytes();
  }

  private renormalize(): void {
    while (this.range < 256) {
      if (this.low < 256) {
        this.putBit(0);
      } else if (this.low >= 512) {
        this.low -= 512;
        this.putBit(1);
      } else {
        this.low -= 256;
        this.bitsOutstanding++;
      }
      this.range <<= 1;
      this.low <<= 1;
    }
  }

  private putBit(bit: Bin): void {
    this.writer.putBit(bit);
    const follow = (1 - bit) as Bin;
    while (this.bitsOutstanding > 0) {
      this.writer.putBit(follow);
      this.bitsOutstanding--;
    }
  }
}
