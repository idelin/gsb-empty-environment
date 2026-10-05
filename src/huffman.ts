// Canonical Huffman codes: a code is fully described by one bit length per
// symbol. Codes are assigned in order of (code length, symbol). See FORMAT.md.

import { BlockError, MAX_CODE_BITS } from './format.ts';
import type { BitReader } from './bitstream.ts';

// Builds length-limited Huffman code lengths for the given frequencies.
// Symbols with zero frequency get length 0. A single used symbol gets length 1
// (an incomplete code, which the format explicitly permits).
export function buildCodeLengths(freqs: ArrayLike<number>, maxBits: number = MAX_CODE_BITS): Uint8Array {
  const n = freqs.length;
  const lens = new Uint8Array(n);
  const used: number[] = [];
  for (let i = 0; i < n; i++) {
    if (freqs[i] > 0) used.push(i);
  }
  if (used.length === 0) return lens;
  if (used.length === 1) {
    lens[used[0]] = 1;
    return lens;
  }

  // Huffman tree via repeated merge of the two lightest nodes.
  interface Node {
    freq: number;
    minSym: number;
    sym: number; // -1 for internal nodes
    left: Node | null;
    right: Node | null;
  }
  const nodes: Node[] = used.map((s) => ({ freq: freqs[s], minSym: s, sym: s, left: null, right: null }));
  while (nodes.length > 1) {
    nodes.sort((a, b) => a.freq - b.freq || a.minSym - b.minSym);
    const a = nodes.shift() as Node;
    const b = nodes.shift() as Node;
    nodes.push({
      freq: a.freq + b.freq,
      minSym: Math.min(a.minSym, b.minSym),
      sym: -1,
      left: a,
      right: b,
    });
  }
  const root = nodes[0];
  const stack: Array<[Node, number]> = [[root, 0]];
  while (stack.length > 0) {
    const [node, depth] = stack.pop() as [Node, number];
    if (node.sym >= 0) {
      lens[node.sym] = Math.max(1, depth);
    } else {
      stack.push([node.left as Node, depth + 1]);
      stack.push([node.right as Node, depth + 1]);
    }
  }

  // Limit lengths to maxBits (zlib-style overflow adjustment), then assign
  // the longest lengths to the least frequent symbols.
  const blCount = new Array<number>(maxBits + 1).fill(0);
  let overflow = 0;
  for (const s of used) {
    let l = lens[s];
    if (l > maxBits) {
      l = maxBits;
      overflow++;
    }
    lens[s] = l;
    blCount[l]++;
  }
  while (overflow > 0) {
    let bits = maxBits - 1;
    while (blCount[bits] === 0) bits--;
    blCount[bits]--;
    blCount[bits + 1] += 2;
    blCount[maxBits]--;
    overflow -= 2;
  }
  const byFreq = [...used].sort((a, b) => freqs[a] - freqs[b] || a - b);
  let idx = 0;
  for (let bits = maxBits; bits >= 1; bits--) {
    for (let j = 0; j < blCount[bits]; j++) {
      lens[byFreq[idx++]] = bits;
    }
  }

  // Defensive fallback: if anything went wrong, use a flat (valid) code.
  try {
    validateCodeLengths(lens, { allowEmpty: false });
  } catch {
    lens.fill(0);
    const flat = Math.max(1, 32 - Math.clz32(used.length - 1));
    for (const s of used) lens[s] = flat;
  }
  return lens;
}

// Validates a code length table. Rejects lengths > MAX_CODE_BITS and
// oversubscribed codes (Kraft sum above 1). Incomplete codes are legal.
export function validateCodeLengths(lens: Uint8Array, opts: { allowEmpty: boolean }): void {
  let used = 0;
  let kraft = 0;
  for (let i = 0; i < lens.length; i++) {
    const l = lens[i];
    if (l > MAX_CODE_BITS) {
      throw new BlockError('CORRUPT', `code length ${l} exceeds ${MAX_CODE_BITS}`);
    }
    if (l > 0) {
      used++;
      kraft += 1 << (MAX_CODE_BITS - l);
    }
  }
  if (used === 0) {
    if (opts.allowEmpty) return;
    throw new BlockError('CORRUPT', 'code table has no symbols');
  }
  if (kraft > 1 << MAX_CODE_BITS) {
    throw new BlockError('OVERSUBSCRIBED', 'code lengths oversubscribe the code space');
  }
}

// Assigns canonical codes: sorted by (length, symbol), consecutive within a
// length, left-shifted between lengths. Returns the code value per symbol.
export function assignCanonicalCodes(lens: Uint8Array): Uint32Array {
  const count = new Array<number>(MAX_CODE_BITS + 1).fill(0);
  for (let i = 0; i < lens.length; i++) {
    if (lens[i] > 0) count[lens[i]]++;
  }
  const next = new Array<number>(MAX_CODE_BITS + 1).fill(0);
  let code = 0;
  for (let bits = 1; bits <= MAX_CODE_BITS; bits++) {
    code = (code + count[bits - 1]) << 1;
    next[bits] = code;
  }
  const codes = new Uint32Array(lens.length);
  for (let s = 0; s < lens.length; s++) {
    if (lens[s] > 0) codes[s] = next[lens[s]]++;
  }
  return codes;
}

export interface HuffDecoder {
  count: Uint16Array; // number of codes per length, index 1..MAX_CODE_BITS
  symbol: Uint16Array; // symbols sorted by (length, symbol)
}

export function makeDecoder(lens: Uint8Array): HuffDecoder {
  const count = new Uint16Array(MAX_CODE_BITS + 1);
  for (let i = 0; i < lens.length; i++) {
    if (lens[i] > 0) count[lens[i]]++;
  }
  const symbol = new Uint16Array(lens.length);
  let idx = 0;
  for (let len = 1; len <= MAX_CODE_BITS; len++) {
    for (let sym = 0; sym < lens.length; sym++) {
      if (lens[sym] === len) symbol[idx++] = sym;
    }
  }
  return { count, symbol };
}

// Decodes one symbol (puff-style canonical walk). Throws BlockError if the
// bit stream ends or names no valid code.
export function decodeSymbol(dec: HuffDecoder, reader: BitReader): number {
  let code = 0;
  let first = 0;
  let index = 0;
  for (let len = 1; len <= MAX_CODE_BITS; len++) {
    code |= reader.readBit();
    const count = dec.count[len];
    if (code - first < count) {
      return dec.symbol[index + (code - first)];
    }
    index += count;
    first = (first + count) << 1;
    code <<= 1;
  }
  throw new BlockError('CORRUPT', 'invalid huffman code in payload');
}
