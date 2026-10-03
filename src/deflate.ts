/**
 * Stage 2: turn LZ77 tokens into a compact RFC 1951 bit stream.
 *
 * Frequencies of the literal/length and distance alphabets feed two
 * length-limited Huffman trees; the code lengths themselves are RLE-coded
 * and compressed with a third (code-length) Huffman tree. Each stream is
 * emitted as whichever of stored / fixed / dynamic blocks is smallest.
 */

import { BitWriter } from "./bitstream.ts";
import { buildHuffman, lengthsToCodes, type HuffmanCode } from "./huffman.ts";
import { lz77Tokenize, type Token } from "./lz77.ts";
import {
  CLCODE_ORDER,
  DIST_BASE,
  DIST_EXTRA,
  END_OF_BLOCK,
  LENGTH_BASE,
  LENGTH_EXTRA,
  MAX_BITS,
  MAX_CL_BITS,
  NUM_CL_CODES,
  NUM_DIST_CODES,
  NUM_LITLEN_CODES,
  distCodeOf,
  lengthCodeOf,
} from "./tables.ts";

export const BLOCK_STORED = 0;
export const BLOCK_FIXED = 1;
export const BLOCK_DYNAMIC = 2;

const MAX_STORED_LEN = 65535;

/** Fixed-Huffman tables from RFC 1951 section 3.2.6. */
export function fixedLitLenLengths(): number[] {
  const lengths = new Array<number>(288);
  for (let i = 0; i <= 143; i++) lengths[i] = 8;
  for (let i = 144; i <= 255; i++) lengths[i] = 9;
  for (let i = 256; i <= 279; i++) lengths[i] = 7;
  for (let i = 280; i <= 287; i++) lengths[i] = 8;
  return lengths;
}

export function fixedDistLengths(): number[] {
  return new Array<number>(32).fill(5);
}

interface TokenStats {
  litLenFreqs: number[];
  distFreqs: number[];
}

function countFrequencies(tokens: Token[]): TokenStats {
  const litLenFreqs = new Array<number>(NUM_LITLEN_CODES).fill(0);
  const distFreqs = new Array<number>(NUM_DIST_CODES).fill(0);
  for (const t of tokens) {
    if (t.distance === 0) {
      litLenFreqs[t.literalOrLength]++;
    } else {
      litLenFreqs[lengthCodeOf(t.literalOrLength)]++;
      distFreqs[distCodeOf(t.distance)]++;
    }
  }
  litLenFreqs[END_OF_BLOCK]++; // exactly one end-of-block marker
  return { litLenFreqs, distFreqs };
}

/** RLE-encode the concatenated code lengths with symbols 16/17/18. */
function rleEncodeCodeLengths(lengths: number[]): Array<{ sym: number; extra: number }> {
  const out: Array<{ sym: number; extra: number }> = [];
  let i = 0;
  while (i < lengths.length) {
    const value = lengths[i];
    let run = 1;
    while (i + run < lengths.length && lengths[i + run] === value) run++;
    i += run;
    if (value === 0) {
      while (run >= 11) {
        const take = Math.min(run, 138);
        out.push({ sym: 18, extra: take - 11 });
        run -= take;
      }
      if (run >= 3) {
        out.push({ sym: 17, extra: run - 3 });
        run = 0;
      }
      while (run-- > 0) out.push({ sym: 0, extra: 0 });
    } else {
      out.push({ sym: value, extra: 0 });
      run--;
      while (run >= 3) {
        const take = Math.min(run, 6);
        out.push({ sym: 16, extra: take - 3 });
        run -= take;
      }
      while (run-- > 0) out.push({ sym: value, extra: 0 });
    }
  }
  return out;
}

function clExtraBits(sym: number): number {
  return sym === 16 ? 2 : sym === 17 ? 3 : sym === 18 ? 7 : 0;
}

interface DynamicPlan {
  litLen: HuffmanCode;
  dist: HuffmanCode;
  cl: HuffmanCode;
  hlit: number;
  hdist: number;
  hclen: number;
  rle: Array<{ sym: number; extra: number }>;
  headerBits: number;
}

function planDynamic(stats: TokenStats): DynamicPlan {
  const litLen = buildHuffman(stats.litLenFreqs, MAX_BITS);
  const dist = buildHuffman(stats.distFreqs, MAX_BITS);

  // Trim trailing zero-length codes (HLIT >= 257, HDIST >= 1 always).
  let hlit = NUM_LITLEN_CODES;
  while (hlit > 257 && litLen.lengths[hlit - 1] === 0) hlit--;
  let hdist = NUM_DIST_CODES;
  while (hdist > 1 && dist.lengths[hdist - 1] === 0) hdist--;

  const combined = litLen.lengths.slice(0, hlit).concat(dist.lengths.slice(0, hdist));
  const rle = rleEncodeCodeLengths(combined);

  const clFreqs = new Array<number>(NUM_CL_CODES).fill(0);
  for (const { sym } of rle) clFreqs[sym]++;
  const cl = buildHuffman(clFreqs, MAX_CL_BITS);

  let hclen = NUM_CL_CODES;
  while (hclen > 4 && cl.lengths[CLCODE_ORDER[hclen - 1]] === 0) hclen--;

  let headerBits = 3 + 5 + 5 + 4 + 3 * hclen;
  for (const { sym } of rle) headerBits += cl.lengths[sym] + clExtraBits(sym);

  return { litLen, dist, cl, hlit, hdist, hclen, rle, headerBits };
}

/** Bits needed to encode all tokens + end-of-block with the given tables. */
function bodyBits(tokens: Token[], litLen: HuffmanCode, dist: HuffmanCode): number {
  let bits = litLen.lengths[END_OF_BLOCK];
  for (const t of tokens) {
    if (t.distance === 0) {
      bits += litLen.lengths[t.literalOrLength];
    } else {
      const lc = lengthCodeOf(t.literalOrLength);
      const dc = distCodeOf(t.distance);
      bits += litLen.lengths[lc] + LENGTH_EXTRA[lc - 257];
      bits += dist.lengths[dc] + DIST_EXTRA[dc];
    }
  }
  return bits;
}

function writeTokens(
  writer: BitWriter,
  tokens: Token[],
  litLen: HuffmanCode,
  dist: HuffmanCode,
): void {
  for (const t of tokens) {
    if (t.distance === 0) {
      writer.writeCode(litLen.codes[t.literalOrLength], litLen.lengths[t.literalOrLength]);
    } else {
      const lc = lengthCodeOf(t.literalOrLength);
      const dc = distCodeOf(t.distance);
      writer.writeCode(litLen.codes[lc], litLen.lengths[lc]);
      writer.writeBits(t.literalOrLength - LENGTH_BASE[lc - 257], LENGTH_EXTRA[lc - 257]);
      writer.writeCode(dist.codes[dc], dist.lengths[dc]);
      writer.writeBits(t.distance - DIST_BASE[dc], DIST_EXTRA[dc]);
    }
  }
  writer.writeCode(litLen.codes[END_OF_BLOCK], litLen.lengths[END_OF_BLOCK]);
}

function writeStoredBlocks(data: Uint8Array): Uint8Array {
  const writer = new BitWriter();
  if (data.length === 0) {
    writer.writeBits(1, 1); // BFINAL
    writer.writeBits(BLOCK_STORED, 2);
    writer.alignToByte();
    writer.writeBits(0, 16); // LEN
    writer.writeBits(0xffff, 16); // NLEN
    return writer.toUint8Array();
  }
  let offset = 0;
  while (offset < data.length) {
    const len = Math.min(MAX_STORED_LEN, data.length - offset);
    const isFinal = offset + len >= data.length;
    writer.writeBits(isFinal ? 1 : 0, 1);
    writer.writeBits(BLOCK_STORED, 2);
    writer.alignToByte();
    writer.writeBits(len, 16);
    writer.writeBits(~len & 0xffff, 16);
    writer.writeBytes(data.subarray(offset, offset + len));
    offset += len;
  }
  return writer.toUint8Array();
}

function writeFixedBlock(tokens: Token[]): Uint8Array {
  const litLen: HuffmanCode = {
    lengths: fixedLitLenLengths(),
    codes: lengthsToCodes(fixedLitLenLengths()),
    maxLength: 9,
  };
  const dist: HuffmanCode = {
    lengths: fixedDistLengths(),
    codes: lengthsToCodes(fixedDistLengths()),
    maxLength: 5,
  };
  const writer = new BitWriter();
  writer.writeBits(1, 1); // BFINAL
  writer.writeBits(BLOCK_FIXED, 2);
  writeTokens(writer, tokens, litLen, dist);
  return writer.toUint8Array();
}

function writeDynamicBlock(tokens: Token[], plan: DynamicPlan): Uint8Array {
  const writer = new BitWriter();
  writer.writeBits(1, 1); // BFINAL
  writer.writeBits(BLOCK_DYNAMIC, 2);
  writer.writeBits(plan.hlit - 257, 5);
  writer.writeBits(plan.hdist - 1, 5);
  writer.writeBits(plan.hclen - 4, 4);
  for (let i = 0; i < plan.hclen; i++) {
    writer.writeBits(plan.cl.lengths[CLCODE_ORDER[i]], 3);
  }
  for (const { sym, extra } of plan.rle) {
    writer.writeCode(plan.cl.codes[sym], plan.cl.lengths[sym]);
    writer.writeBits(extra, clExtraBits(sym));
  }
  writeTokens(writer, tokens, plan.litLen, plan.dist);
  return writer.toUint8Array();
}

export interface DeflateResult {
  data: Uint8Array;
  blockType: number;
  originalSize: number;
}

/**
 * Compress `data` into a raw Deflate stream (RFC 1951), choosing the
 * smallest of stored / fixed-Huffman / dynamic-Huffman encodings.
 */
export function deflate(input: Uint8Array): DeflateResult {
  const data = input instanceof Uint8Array ? input : Uint8Array.from(input);
  const tokens = lz77Tokenize(data);
  const stats = countFrequencies(tokens);

  const dynamicPlan = planDynamic(stats);
  const dynamicBits = dynamicPlan.headerBits + bodyBits(tokens, dynamicPlan.litLen, dynamicPlan.dist);

  const fixedLitLen: HuffmanCode = {
    lengths: fixedLitLenLengths(),
    codes: [],
    maxLength: 9,
  };
  const fixedDist: HuffmanCode = { lengths: fixedDistLengths(), codes: [], maxLength: 5 };
  const fixedBits = 3 + bodyBits(tokens, fixedLitLen, fixedDist);

  const storedBytes =
    data.length === 0 ? 5 : data.length + 5 * Math.ceil(data.length / MAX_STORED_LEN);
  const storedBits = 8 * storedBytes;

  let blockType = BLOCK_DYNAMIC;
  let minBits = dynamicBits;
  if (fixedBits < minBits) {
    minBits = fixedBits;
    blockType = BLOCK_FIXED;
  }
  if (storedBits < minBits) {
    blockType = BLOCK_STORED;
  }

  let out: Uint8Array;
  if (blockType === BLOCK_STORED) out = writeStoredBlocks(data);
  else if (blockType === BLOCK_FIXED) out = writeFixedBlock(tokens);
  else out = writeDynamicBlock(tokens, dynamicPlan);

  return { data: out, blockType, originalSize: data.length };
}

/** Convenience wrapper returning only the compressed bytes. */
export function compress(input: Uint8Array): Uint8Array {
  return deflate(input).data;
}
