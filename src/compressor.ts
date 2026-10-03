import { BitReader, BitWriter } from './bitstream.ts';
import type { BinaryBitmap } from './bitmap.ts';
import {
  BLACK_TABLE,
  ESCAPE_LITERAL_BITS,
  ESCAPE_SYMBOL,
  MAKEUP_STEP,
  MAX_MAKEUP_RUN,
  WHITE_TABLE,
  makeupSymbolFor,
  symbolToRunLength,
  TERMINATOR_COUNT,
} from './tables.ts';

/**
 * Compound compressor: RLE (step 1) + preset Huffman variable-length codes
 * (step 2), Fax Group 3/4 style.
 *
 * Container layout:
 *   [0..3]  magic "RHC1"
 *   [4..7]  width  (uint32 LE)
 *   [8..11] height (uint32 LE)
 *   [12..]  MSB-first bit stream of Huffman-coded run lengths, row by row.
 */

const HEADER_SIZE = 12;
const MAGIC = [0x52, 0x48, 0x43, 0x31]; // "RHC1"

export function compress(bitmap: BinaryBitmap): Uint8Array {
  const { width, height, pixels } = bitmap;
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 0 || height < 0) {
    throw new Error(`Invalid bitmap dimensions ${width}x${height}`);
  }
  if (pixels.length !== width * height) {
    throw new Error(`Pixel buffer length ${pixels.length} does not match ${width}x${height}`);
  }

  const writer = new BitWriter();
  for (let y = 0; y < height; y++) {
    const rowStart = y * width;
    const rowEnd = rowStart + width;
    let x = rowStart;
    let color = 0;
    while (x < rowEnd) {
      // native indexOf skips long same-color stretches in one go
      let next = pixels.indexOf(color ^ 1, x);
      if (next === -1 || next > rowEnd) next = rowEnd;
      writeRun(writer, next - x, color);
      color ^= 1;
      x = next;
    }
  }

  const payload = writer.toUint8Array();
  const out = new Uint8Array(HEADER_SIZE + payload.length);
  out.set(MAGIC, 0);
  const view = new DataView(out.buffer);
  view.setUint32(4, width, true);
  view.setUint32(8, height, true);
  out.set(payload, HEADER_SIZE);
  return out;
}

export function decompress(data: Uint8Array): BinaryBitmap {
  if (data.length < HEADER_SIZE) {
    throw new Error('RHC: stream too short for header');
  }
  for (let i = 0; i < MAGIC.length; i++) {
    if (data[i] !== MAGIC[i]) throw new Error('RHC: bad magic, not an RHC1 stream');
  }
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  const width = view.getUint32(4, true);
  const height = view.getUint32(8, true);

  const pixels = new Uint8Array(width * height);
  const reader = new BitReader(data, HEADER_SIZE);
  for (let y = 0; y < height; y++) {
    let filled = 0;
    let color = 0;
    const rowStart = y * width;
    while (filled < width) {
      const run = readRun(reader, color);
      if (run > width - filled) {
        throw new Error(`RHC: corrupt stream, run of ${run} overflows row ${y} (width ${width})`);
      }
      if (color === 1) pixels.fill(1, rowStart + filled, rowStart + filled + run);
      filled += run;
      color ^= 1;
    }
  }
  return { width, height, pixels };
}

function writeRun(writer: BitWriter, run: number, color: number): void {
  const codes = color === 0 ? WHITE_TABLE.codes : BLACK_TABLE.codes;
  if (run > MAX_MAKEUP_RUN) {
    const escape = codes[ESCAPE_SYMBOL];
    writer.writeBits(escape.code, escape.length);
    writer.writeBits(run, ESCAPE_LITERAL_BITS);
    return;
  }
  let remaining = run;
  while (remaining >= MAKEUP_STEP) {
    const makeup = Math.min(remaining - (remaining % MAKEUP_STEP), MAX_MAKEUP_RUN);
    const code = codes[makeupSymbolFor(makeup)];
    writer.writeBits(code.code, code.length);
    remaining -= makeup;
  }
  const terminator = codes[remaining];
  writer.writeBits(terminator.code, terminator.length);
}

function readRun(reader: BitReader, color: number): number {
  const decoder = color === 0 ? WHITE_TABLE.decoder : BLACK_TABLE.decoder;
  let total = 0;
  for (;;) {
    const symbol = decoder.decode(reader);
    if (symbol === ESCAPE_SYMBOL) {
      return total + reader.readBits(ESCAPE_LITERAL_BITS);
    }
    if (symbol < TERMINATOR_COUNT) {
      return total + symbol;
    }
    total += symbolToRunLength(symbol);
  }
}
