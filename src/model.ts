// Static frequency model: the cumulative probability distribution shared by
// the encoder and the decoder.

export class FrequencyModel {
  readonly symbols: number[];
  // cum[i] is the cumulative frequency of all symbols before symbols[i];
  // cum[symbols.length] equals total.
  readonly cum: number[];
  readonly total: number;
  private lookup: number[];

  constructor(symbols: number[], freqs: number[]) {
    if (symbols.length === 0 || symbols.length !== freqs.length) {
      throw new Error("symbols/freqs length mismatch");
    }
    this.symbols = symbols.slice();
    this.cum = new Array(symbols.length + 1);
    this.lookup = new Array(256).fill(-1);
    let acc: number = 0;
    for (let i: number = 0; i < symbols.length; i++) {
      if (freqs[i] <= 0) {
        throw new Error("frequencies must be positive");
      }
      this.lookup[symbols[i]] = i;
      this.cum[i] = acc;
      acc += freqs[i];
    }
    this.cum[symbols.length] = acc;
    this.total = acc;
    if (this.total > 0x7fffffff) {
      throw new Error("total frequency too large");
    }
  }

  // Build a model from the exact symbol histogram of the data.
  static fromData(data: Uint8Array): FrequencyModel {
    const counts: number[] = new Array(256).fill(0);
    for (let i: number = 0; i < data.length; i++) {
      counts[data[i]] += 1;
    }
    const symbols: number[] = [];
    const freqs: number[] = [];
    for (let s: number = 0; s < 256; s++) {
      if (counts[s] > 0) {
        symbols.push(s);
        freqs.push(counts[s]);
      }
    }
    return new FrequencyModel(symbols, freqs);
  }

  indexOf(symbol: number): number {
    const idx: number = this.lookup[symbol];
    if (idx < 0) {
      throw new Error("symbol not in model: " + symbol);
    }
    return idx;
  }

  cumLow(index: number): number {
    return this.cum[index];
  }

  cumHigh(index: number): number {
    return this.cum[index + 1];
  }

  // Binary search: find i such that cum[i] <= value < cum[i + 1].
  findIndex(value: number): number {
    let lo: number = 0;
    let hi: number = this.symbols.length - 1;
    while (lo < hi) {
      const mid: number = (lo + hi + 1) >> 1;
      if (this.cum[mid] <= value) {
        lo = mid;
      } else {
        hi = mid - 1;
      }
    }
    return lo;
  }
}
