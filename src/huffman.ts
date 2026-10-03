/**
 * Length-limited canonical Huffman codes.
 *
 * Code lengths are built with a binary heap; when the optimal tree exceeds
 * the bit-length limit, the overflow is redistributed using the same
 * algorithm as zlib's gen_bitlen(), keeping the code valid and near-optimal.
 */

export interface HuffmanCode {
  /** codeLengths[sym] = bit length, 0 when the symbol is unused. */
  lengths: number[];
  /** Canonical (MSB-first) code per symbol, valid where lengths[sym] > 0. */
  codes: number[];
  maxLength: number;
}

/**
 * Compute length-limited Huffman code lengths for the given frequencies.
 * Returns an array parallel to `freqs`; unused symbols get length 0.
 * A single used symbol gets a 1-bit code (permitted by RFC 1951 decoders).
 */
export function buildCodeLengths(freqs: readonly number[], maxBits: number): number[] {
  const n = freqs.length;
  const lengths = new Array<number>(n).fill(0);
  const used: number[] = [];
  for (let s = 0; s < n; s++) if (freqs[s] > 0) used.push(s);
  if (used.length === 0) return lengths;
  if (used.length === 1) {
    lengths[used[0]] = 1;
    return lengths;
  }

  // Build a Huffman tree with a binary min-heap of (freq, tiebreak, nodeId).
  // Leaves are node ids 0..n-1; internal nodes get ids n..2n-2.
  const nodeFreq: number[] = [];
  const parent: number[] = [];
  for (let s = 0; s < n; s++) {
    nodeFreq.push(freqs[s]);
    parent.push(-1);
  }

  // Binary heap ordered by (freq, insertion order) for determinism.
  const heapIds: number[] = [];
  const heapKey: number[] = [];
  const less = (i: number, j: number): boolean =>
    heapKey[i] < heapKey[j] || (heapKey[i] === heapKey[j] && heapIds[i] < heapIds[j]);
  const push = (id: number, key: number): void => {
    heapIds.push(id);
    heapKey.push(key);
    let i = heapIds.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (less(i, p)) {
        [heapIds[i], heapIds[p]] = [heapIds[p], heapIds[i]];
        [heapKey[i], heapKey[p]] = [heapKey[p], heapKey[i]];
        i = p;
      } else break;
    }
  };
  const pop = (): number => {
    const top = heapIds[0];
    const lastId = heapIds.pop()!;
    const lastKey = heapKey.pop()!;
    if (heapIds.length > 0) {
      heapIds[0] = lastId;
      heapKey[0] = lastKey;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < heapIds.length && less(l, m)) m = l;
        if (r < heapIds.length && less(r, m)) m = r;
        if (m === i) break;
        [heapIds[i], heapIds[m]] = [heapIds[m], heapIds[i]];
        [heapKey[i], heapKey[m]] = [heapKey[m], heapKey[i]];
        i = m;
      }
    }
    return top;
  };

  for (const s of used) push(s, freqs[s]);
  let nextId = n;
  while (heapIds.length > 1) {
    const a = pop();
    const b = pop();
    const f = nodeFreq[a] + nodeFreq[b];
    nodeFreq.push(f);
    parent.push(-1);
    parent[a] = nextId;
    parent[b] = nextId;
    push(nextId, f);
    nextId++;
  }

  // Leaf depths -> bit-length histogram, clamping to maxBits.
  const blCount = new Array<number>(maxBits + 1).fill(0);
  let overflow = 0;
  for (const s of used) {
    let depth = 0;
    for (let id = s; parent[id] !== -1; id = parent[id]) depth++;
    if (depth > maxBits) {
      depth = maxBits;
      overflow++;
    }
    blCount[depth]++;
  }

  // Redistribute overflow (zlib gen_bitlen): move a leaf one level up as the
  // sibling of two overflow items, pushing them down to the limit.
  while (overflow > 0) {
    let bits = maxBits - 1;
    while (blCount[bits] === 0) bits--;
    blCount[bits]--;
    blCount[bits + 1] += 2;
    blCount[maxBits]--;
    overflow -= 2;
  }

  // Reassign lengths: least-frequent symbols receive the longest codes.
  const byFreq = used.slice().sort((a, b) => freqs[a] - freqs[b] || a - b);
  let idx = 0;
  for (let bits = maxBits; bits >= 1; bits--) {
    for (let k = 0; k < blCount[bits]; k++) lengths[byFreq[idx++]] = bits;
  }
  return lengths;
}

/** Assign canonical codes (RFC 1951 3.2.2) from code lengths. */
export function lengthsToCodes(lengths: readonly number[]): number[] {
  const maxBits = Math.max(0, ...lengths);
  const blCount = new Array<number>(maxBits + 1).fill(0);
  for (const len of lengths) if (len > 0) blCount[len]++;
  const nextCode = new Array<number>(maxBits + 1).fill(0);
  let code = 0;
  for (let bits = 1; bits <= maxBits; bits++) {
    code = (code + blCount[bits - 1]) << 1;
    nextCode[bits] = code;
  }
  const codes = new Array<number>(lengths.length).fill(0);
  for (let s = 0; s < lengths.length; s++) {
    const len = lengths[s];
    if (len > 0) codes[s] = nextCode[len]++;
  }
  return codes;
}

/** Build a complete Huffman code (lengths + canonical codes) from frequencies. */
export function buildHuffman(freqs: readonly number[], maxBits: number): HuffmanCode {
  const lengths = buildCodeLengths(freqs, maxBits);
  const codes = lengthsToCodes(lengths);
  return { lengths, codes, maxLength: Math.max(0, ...lengths) };
}

/** Decoder: canonical-code lookup table keyed by (length, code). */
export class HuffmanDecoder {
  private readonly table = new Map<number, number>();
  readonly maxLength: number;

  readonly lengths: readonly number[];

  constructor(lengths: readonly number[]) {
    this.lengths = lengths;
    const codes = lengthsToCodes(lengths);
    this.maxLength = Math.max(0, ...lengths);
    for (let s = 0; s < lengths.length; s++) {
      const len = lengths[s];
      if (len > 0) this.table.set((len << 16) | codes[s], s);
    }
  }

  get empty(): boolean {
    return this.table.size === 0;
  }

  /** Decode one symbol by consuming bits MSB-first from the reader. */
  decode(readBit: () => number): number {
    let code = 0;
    for (let len = 1; len <= this.maxLength; len++) {
      code = (code << 1) | readBit();
      const sym = this.table.get((len << 16) | code);
      if (sym !== undefined) return sym;
    }
    throw new Error("inflate: invalid huffman code");
  }
}
