import { BitReader } from "./bitio.ts";
import type { Bin, ContextModel } from "./context.ts";
import { RANGE_TAB_LPS } from "./tables.ts";

/**
 * CABAC binary arithmetic decoder, mirroring {@link CabacEncoder}.
 * Context updates are performed identically on both sides, so encoder and
 * decoder probability states stay in lockstep.
 */
export class CabacDecoder {
  private range = 510;
  private value: number;
  private readonly reader: BitReader;

  constructor(bytes: Uint8Array) {
    this.reader = new BitReader(bytes);
    this.value = this.reader.getBits(9);
  }

  /** Decode one bin with the given adaptive regular context. */
  decodeBin(ctx: ContextModel): Bin {
    const rLps = RANGE_TAB_LPS[ctx.stateIdx][(this.range >> 6) & 3];
    this.range -= rLps;
    let bin: Bin;
    if (this.value >= this.range) {
      bin = (1 - ctx.valMPS) as Bin;
      this.value -= this.range;
      this.range = rLps;
      ctx.updateLPS();
    } else {
      bin = ctx.valMPS;
      ctx.updateMPS();
    }
    while (this.range < 256) {
      this.range <<= 1;
      this.value = (this.value << 1) | this.reader.getBit();
    }
    return bin;
  }

  /** Decode one bypass bin (fixed 50/50 probability). */
  decodeBypass(): Bin {
    this.value = (this.value << 1) | this.reader.getBit();
    if (this.value >= this.range) {
      this.value -= this.range;
      return 1;
    }
    return 0;
  }
}
