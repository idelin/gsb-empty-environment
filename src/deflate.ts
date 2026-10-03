// Stage 2: turn LZ77 tokens into a Deflate bit stream (RFC 1951).
// Picks the cheapest of stored / fixed-Huffman / dynamic-Huffman blocks.

import { BitWriter } from './bitstream.ts';
import { buildCodeLengths, lengthsToCodes } from './huffman.ts';
import { tokenize, type Token } from './lz77.ts';
import {
  CL_ORDER,
  DIST_BASE,
  DIST_EXTRA,
  LENGTH_BASE,
  LENGTH_EXTRA,
  MAX_BITS,
  MAX_CL_BITS,
  NUM_CL_CODES,
  NUM_DIST_CODES,
  NUM_LITLEN_CODES,
  distanceToCode,
  fixedDistLengths,
  fixedLitlenLengths,
  lengthToCodeIndex,
} from './tables.ts';

interface ClOp {
  symbol: number; // 0..18
  extraValue: number;
  extraBits: number;
}

// Run-length encode a code-length sequence using symbols 16/17/18.
function encodeCodeLengths(lengths: readonly number[]): ClOp[] {
  const ops: ClOp[] = [];
  let i = 0;
  while (i < lengths.length) {
    const value = lengths[i];
    let run = 1;
    while (i + run < lengths.length && lengths[i + run] === value) run++;
    i += run;
    if (value === 0) {
      while (run >= 11) {
        const use = Math.min(run, 138);
        ops.push({ symbol: 18, extraValue: use - 11, extraBits: 7 });
        run -= use;
      }
      if (run >= 3) {
        ops.push({ symbol: 17, extraValue: run - 3, extraBits: 3 });
        run = 0;
      }
      while (run-- > 0) ops.push({ symbol: 0, extraValue: 0, extraBits: 0 });
    } else {
      ops.push({ symbol: value, extraValue: 0, extraBits: 0 });
      run--;
      while (run >= 3) {
        const use = Math.min(run, 6);
        ops.push({ symbol: 16, extraValue: use - 3, extraBits: 2 });
        run -= use;
      }
      while (run-- > 0) ops.push({ symbol: value, extraValue: 0, extraBits: 0 });
    }
  }
  return ops;
}

interface TokenStats {
  litFreq: number[];
  distFreq: number[];
  extraBitsTotal: number;
}

function collectStats(tokens: readonly Token[]): TokenStats {
  const litFreq = new Array<number>(NUM_LITLEN_CODES).fill(0);
  const distFreq = new Array<number>(NUM_DIST_CODES).fill(0);
  let extraBitsTotal = 0;
  for (const token of tokens) {
    if (token.kind === 'literal') {
      litFreq[token.value]++;
    } else {
      const li = lengthToCodeIndex(token.length);
      const dc = distanceToCode(token.distance);
      litFreq[257 + li]++;
      distFreq[dc]++;
      extraBitsTotal += LENGTH_EXTRA[li] + DIST_EXTRA[dc];
    }
  }
  litFreq[256]++; // end-of-block
  return { litFreq, distFreq, extraBitsTotal };
}

function tokenBits(tokens: readonly Token[], litLen: readonly number[], distLen: readonly number[]): number {
  let bits = litLen[256];
  for (const token of tokens) {
    if (token.kind === 'literal') {
      bits += litLen[token.value];
    } else {
      bits += litLen[257 + lengthToCodeIndex(token.length)];
      bits += LENGTH_EXTRA[lengthToCodeIndex(token.length)];
      bits += distLen[distanceToCode(token.distance)];
      bits += DIST_EXTRA[distanceToCode(token.distance)];
    }
  }
  return bits;
}

function storedBits(dataLen: number): number {
  let bits = 0;
  let remaining = dataLen;
  for (;;) {
    const chunk = Math.min(remaining, 65535);
    bits += 3;
    bits = Math.ceil(bits / 8) * 8; // align
    bits += 32 + chunk * 8;
    remaining -= chunk;
    if (remaining <= 0) break;
  }
  return bits;
}

function writeTokens(
  writer: BitWriter,
  tokens: readonly Token[],
  litCodes: readonly number[],
  litLen: readonly number[],
  distCodes: readonly number[],
  distLen: readonly number[],
): void {
  for (const token of tokens) {
    if (token.kind === 'literal') {
      writer.writeCode(litCodes[token.value], litLen[token.value]);
    } else {
      const li = lengthToCodeIndex(token.length);
      writer.writeCode(litCodes[257 + li], litLen[257 + li]);
      writer.writeBits(token.length - LENGTH_BASE[li], LENGTH_EXTRA[li]);
      const dc = distanceToCode(token.distance);
      writer.writeCode(distCodes[dc], distLen[dc]);
      writer.writeBits(token.distance - DIST_BASE[dc], DIST_EXTRA[dc]);
    }
  }
  writer.writeCode(litCodes[256], litLen[256]); // end-of-block
}

function emitStored(writer: BitWriter, data: Buffer | Uint8Array): void {
  let offset = 0;
  for (;;) {
    const chunk = Math.min(data.length - offset, 65535);
    const last = offset + chunk >= data.length;
    writer.writeBits(last ? 1 : 0, 1);
    writer.writeBits(0, 2); // BTYPE=00
    writer.alignToByte();
    writer.writeBits(chunk, 16);
    writer.writeBits(~chunk & 0xffff, 16);
    writer.writeBytes(data.subarray(offset, offset + chunk));
    offset += chunk;
    if (last) break;
  }
}

function emitFixed(writer: BitWriter, tokens: readonly Token[]): void {
  const litLen = fixedLitlenLengths();
  const distLen = fixedDistLengths();
  writer.writeBits(1, 1); // BFINAL
  writer.writeBits(1, 2); // BTYPE=01
  writeTokens(writer, tokens, lengthsToCodes(litLen), litLen, lengthsToCodes(distLen), distLen);
}

function emitDynamic(
  writer: BitWriter,
  tokens: readonly Token[],
  litLen: readonly number[],
  distLen: readonly number[],
  clOps: readonly ClOp[],
  clLen: readonly number[],
  hlit: number,
  hdist: number,
  hclen: number,
): void {
  writer.writeBits(1, 1); // BFINAL
  writer.writeBits(2, 2); // BTYPE=10
  writer.writeBits(hlit - 257, 5);
  writer.writeBits(hdist - 1, 5);
  writer.writeBits(hclen - 4, 4);
  for (let i = 0; i < hclen; i++) writer.writeBits(clLen[CL_ORDER[i]], 3);
  const clCodes = lengthsToCodes(clLen);
  for (const op of clOps) {
    writer.writeCode(clCodes[op.symbol], clLen[op.symbol]);
    if (op.extraBits > 0) writer.writeBits(op.extraValue, op.extraBits);
  }
  writeTokens(writer, tokens, lengthsToCodes(litLen), litLen, lengthsToCodes(distLen), distLen);
}

export function deflate(data: Buffer | Uint8Array): Buffer {
  const input = Buffer.isBuffer(data) ? data : Buffer.from(data);
  const tokens = tokenize(input);
  const stats = collectStats(tokens);

  // Guarantee at least one distance code (single 1-bit code is legal).
  if (stats.distFreq.every((f) => f === 0)) stats.distFreq[0] = 1;

  const litLen = buildCodeLengths(stats.litFreq, MAX_BITS);
  const distLen = buildCodeLengths(stats.distFreq, MAX_BITS);

  let hlit = NUM_LITLEN_CODES;
  while (hlit > 257 && litLen[hlit - 1] === 0) hlit--;
  let hdist = NUM_DIST_CODES;
  while (hdist > 1 && distLen[hdist - 1] === 0) hdist--;

  const clOps = encodeCodeLengths(litLen.slice(0, hlit).concat(distLen.slice(0, hdist)));
  const clFreq = new Array<number>(NUM_CL_CODES).fill(0);
  for (const op of clOps) clFreq[op.symbol]++;
  const clLen = buildCodeLengths(clFreq, MAX_CL_BITS);
  let hclen = NUM_CL_CODES;
  while (hclen > 4 && clLen[CL_ORDER[hclen - 1]] === 0) hclen--;

  let dynamicBits = 3 + 5 + 5 + 4 + 3 * hclen;
  for (const op of clOps) dynamicBits += clLen[op.symbol] + op.extraBits;
  dynamicBits += tokenBits(tokens, litLen, distLen);

  const fixedBits = 3 + tokenBits(tokens, fixedLitlenLengths(), fixedDistLengths());
  const stored = storedBits(input.length);

  const writer = new BitWriter();
  if (stored <= fixedBits && stored <= dynamicBits) {
    emitStored(writer, input);
  } else if (fixedBits <= dynamicBits) {
    emitFixed(writer, tokens);
  } else {
    emitDynamic(writer, tokens, litLen, distLen, clOps, clLen, hlit, hdist, hclen);
  }
  return writer.toBuffer();
}
