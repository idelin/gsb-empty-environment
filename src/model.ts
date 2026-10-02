/**
 * Adaptive order-0 probability model for a binary alphabet (symbols 0 and 1).
 *
 * Both encoder and decoder keep identical copies of this model and update it
 * after every symbol, so the cumulative distribution stays in sync and the
 * decoder can invert the encoder's interval subdivision exactly.
 */
export class AdaptiveBinaryModel {
  private count0 = 1n;
  private count1 = 1n;
  private total = 2n;

  /** Rescale counts when the total grows too large (keeps adaptation local). */
  private static readonly MAX_TOTAL = 1n << 24n;

  /** Cumulative interval [cumLow, cumHigh) of `symbol` out of `total`. */
  cumulative(symbol: 0 | 1): { cumLow: bigint; cumHigh: bigint; total: bigint } {
    if (symbol === 0) {
      return { cumLow: 0n, cumHigh: this.count0, total: this.total };
    }
    return { cumLow: this.count0, cumHigh: this.total, total: this.total };
  }

  /** Map a cumulative frequency back to a symbol. */
  symbolForCumulative(cum: bigint): 0 | 1 {
    return cum < this.count0 ? 0 : 1;
  }

  update(symbol: 0 | 1): void {
    if (symbol === 0) this.count0++;
    else this.count1++;
    this.total++;
    if (this.total >= AdaptiveBinaryModel.MAX_TOTAL) {
      this.count0 = (this.count0 >> 1n) + 1n;
      this.count1 = (this.count1 >> 1n) + 1n;
      this.total = this.count0 + this.count1;
    }
  }
}
