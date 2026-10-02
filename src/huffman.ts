/**
 * Preset Huffman code tables for run lengths, modelled after the
 * CCITT T.4 (Fax Group 3) white/black run-length code tables.
 *
 * Instead of shipping static tables, the codec derives a canonical
 * codebook from a fixed, preset frequency model that is tuned for
 * whitespace-dominated technical drawings. Encoder and decoder run the
 * same deterministic construction, so both sides always agree on the
 * exact same codes without transmitting any table.
 *
 * Symbol layout (shared by every table):
 *   symbols   0..63   terminating codes: run length 0..63
 *   symbols  64..126  make-up codes: run length (symbol - 63) * 64,
 *                     i.e. 64, 128, ..., 4032
 * Runs longer than 63 are emitted as one or more make-up codes followed
 * by a terminating code, exactly like T.4.
 */

import { BitReader } from './bitstream.ts';

export const MAX_TERMINATING_LENGTH = 63;
export const MAKEUP_STEP = 64;
export const MAX_MAKEUP_INDEX = 63;
export const MAX_MAKEUP_LENGTH = MAKEUP_STEP * MAX_MAKEUP_INDEX; // 4032
export const SYMBOL_COUNT = MAX_TERMINATING_LENGTH + 1 + MAX_MAKEUP_INDEX; // 127

export interface HuffmanTable {
  /** Huffman code value per symbol. */
  readonly codes: Uint32Array;
  /** Code length in bits per symbol. */
  readonly lengths: Uint8Array;
  /** Longest code in the table. */
  readonly maxLength: number;
}

interface HuffmanNode {
  freq: number;
  minSymbol: number;
  symbol: number; // -1 for internal nodes
  left: HuffmanNode | null;
  right: HuffmanNode | null;
}

/**
 * Build a Huffman codebook from preset frequencies.
 * Deterministic: ties are broken by the smallest symbol contained in a
 * subtree, so the same input always yields the same codes.
 */
export function buildHuffmanTable(frequencies: ArrayLike<number>): HuffmanTable {
  const count = frequencies.length;
  const heap: HuffmanNode[] = [];

  const less = (a: HuffmanNode, b: HuffmanNode): boolean =>
    a.freq < b.freq || (a.freq === b.freq && a.minSymbol < b.minSymbol);

  const push = (node: HuffmanNode): void => {
    heap.push(node);
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (less(heap[i], heap[parent])) {
        [heap[i], heap[parent]] = [heap[parent], heap[i]];
        i = parent;
      } else {
        break;
      }
    }
  };

  const pop = (): HuffmanNode => {
    const top = heap[0];
    const last = heap.pop() as HuffmanNode;
    if (heap.length > 0) {
      heap[0] = last;
      let i = 0;
      for (;;) {
        const l = i * 2 + 1;
        const r = l + 1;
        let smallest = i;
        if (l < heap.length && less(heap[l], heap[smallest])) smallest = l;
        if (r < heap.length && less(heap[r], heap[smallest])) smallest = r;
        if (smallest === i) break;
        [heap[i], heap[smallest]] = [heap[smallest], heap[i]];
        i = smallest;
      }
    }
    return top;
  };

  for (let symbol = 0; symbol < count; symbol++) {
    const freq = Math.max(1, Math.floor(frequencies[symbol]));
    push({ freq, minSymbol: symbol, symbol, left: null, right: null });
  }

  while (heap.length > 1) {
    const a = pop();
    const b = pop();
    push({
      freq: a.freq + b.freq,
      minSymbol: Math.min(a.minSymbol, b.minSymbol),
      symbol: -1,
      left: a,
      right: b,
    });
  }

  const codes = new Uint32Array(count);
  const lengths = new Uint8Array(count);
  let maxLength = 0;

  const assign = (node: HuffmanNode, code: number, depth: number): void => {
    if (node.symbol >= 0) {
      // A single-symbol table still gets a 1-bit code.
      const length = Math.max(1, depth);
      codes[node.symbol] = code;
      lengths[node.symbol] = length;
      if (length > maxLength) maxLength = length;
      return;
    }
    assign(node.left as HuffmanNode, code << 1, depth + 1);
    assign(node.right as HuffmanNode, (code << 1) | 1, depth + 1);
  };
  assign(heap[0], 0, 0);

  if (maxLength > 24) {
    throw new Error(`Huffman table too deep (${maxLength} bits); adjust preset frequencies`);
  }
  return { codes, lengths, maxLength };
}

/**
 * Fast decoder built from a table: a direct-addressed look-up table
 * indexed by the next `maxLength` bits of the stream, so each symbol
 * decodes with a single peek + consume instead of a bit-by-bit walk.
 */
export interface HuffmanDecoder {
  readonly bits: number;
  readSymbol(reader: BitReader): number;
}

export function buildHuffmanDecoder(table: HuffmanTable): HuffmanDecoder {
  const bits = table.maxLength;
  const size = 2 ** bits;
  const symbols = new Uint16Array(size);
  const lengths = new Uint8Array(size);

  for (let symbol = 0; symbol < table.codes.length; symbol++) {
    const length = table.lengths[symbol];
    if (length === 0) continue;
    const span = 2 ** (bits - length);
    const start = table.codes[symbol] * span;
    for (let index = start; index < start + span; index++) {
      symbols[index] = symbol;
      lengths[index] = length;
    }
  }

  return {
    bits,
    readSymbol(reader: BitReader): number {
      const index = reader.peekBits(bits);
      const length = lengths[index];
      if (length === 0 || reader.remainingBits < length) {
        throw new Error('Corrupt stream: invalid Huffman code');
      }
      reader.consume(length);
      return symbols[index];
    },
  };
}

/* ------------------------------------------------------------------ */
/* Preset frequency models (the "optimal preset tables" of the codec)  */
/* ------------------------------------------------------------------ */

function makeFrequencies(
  terminating: (length: number) => number,
  makeup: (index: number) => number,
): Float64Array {
  const freq = new Float64Array(SYMBOL_COUNT);
  for (let length = 0; length <= MAX_TERMINATING_LENGTH; length++) {
    freq[length] = Math.max(1, Math.round(terminating(length)));
  }
  for (let index = 1; index <= MAX_MAKEUP_INDEX; index++) {
    freq[MAX_TERMINATING_LENGTH + index] = Math.max(1, Math.round(makeup(index)));
  }
  return freq;
}

/**
 * White runs in technical drawings: mostly very long (blank paper),
 * so large make-up codes are frequent; short terminating runs are rare.
 */
export function whiteRunFrequencies(): Float64Array {
  return makeFrequencies(
    (length) => (length === 0 ? 16 : 64 / (length + 1)),
    (index) => 4 + index / 4, // longer blank spans are more likely
  );
}

/**
 * Black runs in line art: strokes and text are a few pixels wide,
 * so short runs dominate and long make-ups are rare.
 */
export function blackRunFrequencies(): Float64Array {
  return makeFrequencies(
    (length) => (length === 0 ? 1 : 64 / length),
    (index) => 8 / index,
  );
}

/** Neutral table for run lengths of low-colour-depth images. */
export function neutralRunFrequencies(): Float64Array {
  return makeFrequencies(
    (length) => (length === 0 ? 2 : 16 / length),
    (index) => 1 + index / 4,
  );
}

export interface RunLengthTables {
  white: HuffmanTable;
  black: HuffmanTable;
  neutral: HuffmanTable;
}

let cached: RunLengthTables | null = null;

/** The shared preset tables (built once, reused by encode and decode). */
export function presetTables(): RunLengthTables {
  if (cached === null) {
    cached = {
      white: buildHuffmanTable(whiteRunFrequencies()),
      black: buildHuffmanTable(blackRunFrequencies()),
      neutral: buildHuffmanTable(neutralRunFrequencies()),
    };
  }
  return cached;
}
