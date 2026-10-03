import { BitReader } from './bitstream.ts';

export interface HuffmanCode {
  code: number;
  length: number;
}

/**
 * Builds Huffman code lengths for the given symbol frequencies.
 * Deterministic: frequency ties are broken by node index.
 */
export function buildCodeLengths(frequencies: readonly number[], maxLength = 30): Uint8Array {
  const symbolCount = frequencies.length;
  const lengths = new Uint8Array(symbolCount);
  const parents = new Int32Array(2 * symbolCount).fill(-1);
  const freq = new Float64Array(2 * symbolCount);

  const heap: number[] = [];
  const less = (a: number, b: number): boolean =>
    freq[a] < freq[b] || (freq[a] === freq[b] && a < b);
  const heapPush = (value: number): void => {
    heap.push(value);
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!less(heap[i], heap[parent])) break;
      [heap[i], heap[parent]] = [heap[parent], heap[i]];
      i = parent;
    }
  };
  const heapPop = (): number => {
    const top = heap[0];
    const last = heap.pop() as number;
    if (heap.length > 0) {
      heap[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < heap.length && less(heap[left], heap[smallest])) smallest = left;
        if (right < heap.length && less(heap[right], heap[smallest])) smallest = right;
        if (smallest === i) break;
        [heap[i], heap[smallest]] = [heap[smallest], heap[i]];
        i = smallest;
      }
    }
    return top;
  };

  for (let s = 0; s < symbolCount; s++) {
    if (frequencies[s] > 0) {
      freq[s] = frequencies[s];
      heapPush(s);
    }
  }
  if (heap.length === 0) return lengths;
  if (heap.length === 1) {
    lengths[heap[0]] = 1;
    return lengths;
  }

  let next = symbolCount;
  while (heap.length > 1) {
    const a = heapPop();
    const b = heapPop();
    parents[a] = next;
    parents[b] = next;
    freq[next] = freq[a] + freq[b];
    heapPush(next);
    next += 1;
  }

  for (let s = 0; s < symbolCount; s++) {
    if (frequencies[s] === 0) continue;
    let depth = 0;
    let current = s;
    while (parents[current] !== -1) {
      depth += 1;
      current = parents[current];
    }
    if (depth > maxLength) {
      throw new Error(`Huffman code length ${depth} exceeds limit ${maxLength}`);
    }
    lengths[s] = depth;
  }
  return lengths;
}

/**
 * Assigns canonical Huffman codes: symbols ordered by (code length, symbol id)
 * receive consecutive codes, so the table can be regenerated deterministically.
 */
export function assignCanonicalCodes(lengths: Uint8Array): HuffmanCode[] {
  const symbols: number[] = [];
  for (let s = 0; s < lengths.length; s++) {
    if (lengths[s] > 0) symbols.push(s);
  }
  symbols.sort((a, b) => lengths[a] - lengths[b] || a - b);

  const codes: HuffmanCode[] = new Array(lengths.length);
  for (let s = 0; s < lengths.length; s++) codes[s] = { code: 0, length: 0 };

  let code = 0;
  let prevLength = 0;
  for (const symbol of symbols) {
    const length = lengths[symbol];
    code = (code << (length - prevLength)) >>> 0;
    codes[symbol] = { code, length };
    code = (code + 1) >>> 0;
    prevLength = length;
  }
  return codes;
}

/**
 * Binary-tree decoder for canonical Huffman codes.
 * Tree slots: 0 = empty, >0 = child node index, <0 = leaf (-(symbol + 1)).
 */
export class HuffmanDecoder {
  private readonly tree: Int32Array;

  constructor(codes: readonly HuffmanCode[]) {
    const flat: number[] = [0, 0];
    let nodeCount = 0;
    for (let symbol = 0; symbol < codes.length; symbol++) {
      const { code, length } = codes[symbol];
      if (length === 0) continue;
      let node = 0;
      for (let i = length - 1; i >= 0; i--) {
        const bit = (code >>> i) & 1;
        const slot = node * 2 + bit;
        if (i === 0) {
          if (flat[slot] !== 0) throw new Error('Huffman: code collision');
          flat[slot] = -(symbol + 1);
        } else {
          const next = flat[slot];
          if (next < 0) throw new Error('Huffman: prefix conflict');
          if (next === 0) {
            nodeCount += 1;
            flat[slot] = nodeCount;
            flat.push(0, 0);
          }
          node = flat[slot];
        }
      }
    }
    this.tree = Int32Array.from(flat);
  }

  decode(reader: BitReader): number {
    let node = 0;
    for (;;) {
      const bit = reader.readBit();
      const value = this.tree[node * 2 + bit];
      if (value < 0) return -value - 1;
      if (value === 0) throw new Error('Huffman: invalid code in stream');
      node = value;
    }
  }
}
