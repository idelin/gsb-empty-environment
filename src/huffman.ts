// Length-limited Huffman code construction (package-merge) plus
// canonical code assignment and a canonical decoder.

interface PackageItem {
  weight: number;
  symbols: number[]; // leaf symbol indices contained in this item
}

function mergeSorted(a: PackageItem[], b: PackageItem[]): PackageItem[] {
  const out: PackageItem[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i].weight <= b[j].weight) out.push(a[i++]);
    else out.push(b[j++]);
  }
  while (i < a.length) out.push(a[i++]);
  while (j < b.length) out.push(b[j++]);
  return out;
}

// Compute optimal code lengths limited to `maxBits` for the given
// frequencies. Returns an array of the same length as `freqs`;
// unused symbols get length 0.
export function buildCodeLengths(freqs: readonly number[], maxBits: number): number[] {
  const lengths = new Array<number>(freqs.length).fill(0);
  const leaves: PackageItem[] = [];
  for (let sym = 0; sym < freqs.length; sym++) {
    if (freqs[sym] > 0) leaves.push({ weight: freqs[sym], symbols: [sym] });
  }
  if (leaves.length === 0) return lengths;
  if (leaves.length === 1) {
    lengths[leaves[0].symbols[0]] = 1;
    return lengths;
  }
  leaves.sort((a, b) => a.weight - b.weight);

  let list = leaves;
  for (let depth = 1; depth < maxBits; depth++) {
    const packages: PackageItem[] = [];
    for (let i = 0; i + 1 < list.length; i += 2) {
      packages.push({
        weight: list[i].weight + list[i + 1].weight,
        symbols: list[i].symbols.concat(list[i + 1].symbols),
      });
    }
    list = mergeSorted(leaves, packages);
  }

  // The first 2n-2 items of the final list give each symbol its depth.
  const needed = 2 * leaves.length - 2;
  for (let i = 0; i < needed; i++) {
    for (const sym of list[i].symbols) lengths[sym]++;
  }
  return lengths;
}

// Assign canonical codes (RFC 1951 section 3.2.2) for the given lengths.
// Codes are returned MSB-first (not yet bit-reversed for writing).
export function lengthsToCodes(lengths: readonly number[]): number[] {
  const maxBits = Math.max(...lengths);
  const blCount = new Array<number>(maxBits + 1).fill(0);
  for (const len of lengths) {
    if (len > 0) blCount[len]++;
  }
  const nextCode = new Array<number>(maxBits + 1).fill(0);
  let code = 0;
  for (let bits = 1; bits <= maxBits; bits++) {
    code = (code + blCount[bits - 1]) << 1;
    nextCode[bits] = code;
  }
  const codes = new Array<number>(lengths.length).fill(0);
  for (let sym = 0; sym < lengths.length; sym++) {
    const len = lengths[sym];
    if (len > 0) codes[sym] = nextCode[len]++;
  }
  return codes;
}

// Canonical Huffman decoder using the count/offset method from zlib's
// puff.c. Tolerates incomplete codes (only used codes must be valid).
export class HuffmanDecoder {
  private readonly counts: number[];
  private readonly symbols: number[];
  private readonly maxBits: number;

  constructor(lengths: readonly number[]) {
    this.maxBits = Math.max(0, ...lengths);
    this.counts = new Array<number>(this.maxBits + 1).fill(0);
    for (const len of lengths) {
      if (len > 0) this.counts[len]++;
    }
    this.symbols = [];
    for (let len = 1; len <= this.maxBits; len++) {
      for (let sym = 0; sym < lengths.length; sym++) {
        if (lengths[sym] === len) this.symbols.push(sym);
      }
    }
  }

  // `readBit` must return the next stream bit (0 or 1).
  decode(readBit: () => number): number {
    let code = 0;
    let first = 0;
    let index = 0;
    for (let len = 1; len <= this.maxBits; len++) {
      code |= readBit();
      const count = this.counts[len];
      if (code - first < count) return this.symbols[index + (code - first)];
      index += count;
      first = (first + count) << 1;
      code <<= 1;
    }
    throw new Error('inflate: invalid huffman code');
  }
}
