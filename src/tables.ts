import {
  assignCanonicalCodes,
  buildCodeLengths,
  HuffmanDecoder,
  type HuffmanCode,
} from './huffman.ts';

/**
 * Fax Group 3/4 (T.4/T.6) style preset code tables.
 *
 * Run lengths are split exactly like Modified Huffman:
 *   run = make-up code (multiple of 64) + terminating code (0..63).
 * Runs above MAX_MAKEUP_RUN use an escape symbol followed by a 32-bit literal.
 *
 * The two tables (white / black) are *preset*: they are built once from a
 * fixed frequency model of technical drawings (long white runs dominate,
 * black runs are mostly 1-8 px strokes), not from the image being compressed,
 * so no table needs to be stored in the bitstream.
 */

export const TERMINATOR_COUNT = 64;
export const MAKEUP_STEP = 64;
export const MAKEUP_COUNT = 128;
export const MAX_MAKEUP_RUN = MAKEUP_STEP * MAKEUP_COUNT; // 8192
export const ESCAPE_SYMBOL = TERMINATOR_COUNT + MAKEUP_COUNT; // 192
export const ALPHABET_SIZE = ESCAPE_SYMBOL + 1;
export const ESCAPE_LITERAL_BITS = 32;

export function symbolToRunLength(symbol: number): number {
  if (symbol < TERMINATOR_COUNT) return symbol;
  return (symbol - TERMINATOR_COUNT + 1) * MAKEUP_STEP;
}

export function makeupSymbolFor(runLength: number): number {
  return runLength / MAKEUP_STEP + TERMINATOR_COUNT - 1;
}

export interface PresetTable {
  readonly codes: readonly HuffmanCode[];
  readonly decoder: HuffmanDecoder;
}

function buildTable(frequencies: readonly number[]): PresetTable {
  const lengths = buildCodeLengths(frequencies);
  const codes = assignCanonicalCodes(lengths);
  return { codes, decoder: new HuffmanDecoder(codes) };
}

function whiteFrequencies(): number[] {
  const freq = new Array<number>(ALPHABET_SIZE).fill(0);
  for (let t = 0; t < TERMINATOR_COUNT; t++) freq[t] = 1 / (t + 1);
  for (let k = 1; k <= MAKEUP_COUNT; k++) freq[TERMINATOR_COUNT + k - 1] = 0.5 / k;
  freq[ESCAPE_SYMBOL] = 1e-6;
  return freq;
}

function blackFrequencies(): number[] {
  const freq = new Array<number>(ALPHABET_SIZE).fill(0);
  for (let t = 0; t < TERMINATOR_COUNT; t++) freq[t] = 1 / ((t + 1) * (t + 1));
  for (let k = 1; k <= MAKEUP_COUNT; k++) freq[TERMINATOR_COUNT + k - 1] = 1e-3 / k;
  freq[ESCAPE_SYMBOL] = 1e-6;
  return freq;
}

export const WHITE_TABLE: PresetTable = buildTable(whiteFrequencies());
export const BLACK_TABLE: PresetTable = buildTable(blackFrequencies());
