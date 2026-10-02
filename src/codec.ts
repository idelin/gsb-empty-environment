/**
 * Composite run-length + Huffman raster compressor.
 *
 * Stage 1 (RLE): every scanline is turned into a sequence of run
 *   lengths. Binary images alternate white/black runs starting with
 *   white, exactly like Fax Group 3/4; low-colour-depth images store
 *   (value, length) pairs.
 * Stage 2 (Huffman): run lengths are mapped onto preset variable-length
 *   Huffman codes (see huffman.ts), with T.4-style make-up codes for
 *   runs longer than 63 pixels.
 *
 * Bitstream layout:
 *   header  16 bytes: magic "RLH1", width u32le, height u32le,
 *                     bitDepth u8, flags u8 (reserved, 0), reserved u16le
 *   payload row 0 .. height-1, MSB-first bit stream, zero-padded to a
 *           byte boundary at the very end. No EOL markers (G4-style):
 *           the decoder knows each row is complete after `width` pixels.
 */

import { BitReader, BitWriter } from './bitstream.ts';
import {
  MAX_MAKEUP_INDEX,
  MAX_TERMINATING_LENGTH,
  MAKEUP_STEP,
  buildHuffmanDecoder,
  presetTables,
  type HuffmanTable,
} from './huffman.ts';

export type BitDepth = 1 | 2 | 4 | 8;

export interface RasterImage {
  width: number;
  height: number;
  bitDepth: BitDepth;
  /** Unpacked pixels, row-major, one byte per pixel (values 0..2^bitDepth-1). */
  pixels: Uint8Array;
}

export interface CodecHeader {
  width: number;
  height: number;
  bitDepth: BitDepth;
}

const MAGIC = 0x524c4831; // "RLH1"
const HEADER_BYTES = 16;
const VALID_DEPTHS: readonly number[] = [1, 2, 4, 8];

function assertValidShape(image: RasterImage): void {
  const { width, height, bitDepth, pixels } = image;
  if (!Number.isInteger(width) || width <= 0) throw new Error(`Invalid width: ${width}`);
  if (!Number.isInteger(height) || height <= 0) throw new Error(`Invalid height: ${height}`);
  if (!VALID_DEPTHS.includes(bitDepth)) throw new Error(`Invalid bitDepth: ${bitDepth}`);
  if (pixels.length !== width * height) {
    throw new Error(`Pixel buffer length ${pixels.length} does not match ${width}x${height}`);
  }
}

/** Write one run length as make-up code(s) + terminating code. */
function writeRunLength(writer: BitWriter, table: HuffmanTable, runLength: number): void {
  let remaining = runLength;
  while (remaining > MAX_TERMINATING_LENGTH) {
    const index = Math.min(Math.floor(remaining / MAKEUP_STEP), MAX_MAKEUP_INDEX);
    const symbol = MAX_TERMINATING_LENGTH + index;
    writer.writeBits(table.codes[symbol], table.lengths[symbol]);
    remaining -= index * MAKEUP_STEP;
  }
  writer.writeBits(table.codes[remaining], table.lengths[remaining]);
}

/** Read one run length (make-up codes accumulate until a terminating code). */
function readRunLength(readSymbol: () => number): number {
  let total = 0;
  let makeups = 0;
  for (;;) {
    const symbol = readSymbol();
    if (symbol > MAX_TERMINATING_LENGTH) {
      total += (symbol - MAX_TERMINATING_LENGTH) * MAKEUP_STEP;
      if (++makeups > 1 << 16) throw new Error('Corrupt stream: runaway make-up sequence');
    } else {
      return total + symbol;
    }
  }
}

export function compress(image: RasterImage): Uint8Array {
  assertValidShape(image);
  const { width, height, bitDepth, pixels } = image;
  const tables = presetTables();
  const writer = new BitWriter();
  let maxValueSeen = 0;

  for (let y = 0; y < height; y++) {
    const rowStart = y * width;
    let x = 0;
    let color = 0; // decoder expects every row to start with a white run
    while (x < width) {
      const value = pixels[rowStart + x];
      if (value > maxValueSeen) maxValueSeen = value;
      let runEnd = x + 1;
      while (runEnd < width && pixels[rowStart + runEnd] === value) runEnd++;
      const runLength = runEnd - x;
      if (bitDepth === 1) {
        // Runs strictly alternate, starting with white: the colour is
        // implicit, only the length is coded (as in T.4/T.6). A row
        // starting with black is prefixed by a zero-length white run.
        if (value !== color) {
          writeRunLength(writer, color === 0 ? tables.white : tables.black, 0);
          color ^= 1;
        }
        writeRunLength(writer, color === 0 ? tables.white : tables.black, runLength);
        color ^= 1;
      } else {
        writer.writeBits(value, bitDepth);
        writeRunLength(writer, tables.neutral, runLength);
      }
      x = runEnd;
    }
  }

  const maxValue = (1 << bitDepth) - 1;
  if (maxValueSeen > maxValue) {
    throw new Error(`Pixel value ${maxValueSeen} exceeds bitDepth ${bitDepth}`);
  }

  const payload = writer.toBytes();
  const out = new Uint8Array(HEADER_BYTES + payload.length);
  const view = new DataView(out.buffer);
  view.setUint32(0, MAGIC, false);
  view.setUint32(4, width, true);
  view.setUint32(8, height, true);
  view.setUint8(12, bitDepth);
  // bytes 13..15: flags + reserved, left as zero
  out.set(payload, HEADER_BYTES);
  return out;
}

export function decompress(data: Uint8Array): RasterImage {
  if (data.length < HEADER_BYTES) throw new Error('Corrupt stream: too short for header');
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  if (view.getUint32(0, false) !== MAGIC) throw new Error('Corrupt stream: bad magic');
  const width = view.getUint32(4, true);
  const height = view.getUint32(8, true);
  const bitDepth = view.getUint8(12) as BitDepth;
  if (!VALID_DEPTHS.includes(bitDepth)) throw new Error(`Corrupt stream: bad bitDepth ${bitDepth}`);
  if (width === 0 || height === 0) throw new Error('Corrupt stream: zero dimension');

  const tables = presetTables();
  const reader = new BitReader(data.subarray(HEADER_BYTES));
  const whiteDecoder = buildHuffmanDecoder(tables.white);
  const blackDecoder = buildHuffmanDecoder(tables.black);
  const neutralDecoder = buildHuffmanDecoder(tables.neutral);
  const readWhite = () => whiteDecoder.readSymbol(reader);
  const readBlack = () => blackDecoder.readSymbol(reader);
  const readNeutral = () => neutralDecoder.readSymbol(reader);

  const pixels = new Uint8Array(width * height);

  for (let y = 0; y < height; y++) {
    const rowStart = y * width;
    let x = 0;
    if (bitDepth === 1) {
      let color = 0; // every row starts with a white run (possibly length 0)
      while (x < width) {
        const runLength = readRunLength(color === 0 ? readWhite : readBlack);
        if (x + runLength > width) throw new Error(`Corrupt stream: row ${y} overflows width`);
        if (color === 1) pixels.fill(1, rowStart + x, rowStart + x + runLength);
        x += runLength;
        color ^= 1;
      }
    } else {
      while (x < width) {
        const value = reader.readBits(bitDepth);
        const runLength = readRunLength(readNeutral);
        if (x + runLength > width) throw new Error(`Corrupt stream: row ${y} overflows width`);
        if (value !== 0) pixels.fill(value, rowStart + x, rowStart + x + runLength);
        x += runLength;
      }
    }
  }

  // Only zero padding bits may remain after the final row.
  const remaining = reader.remainingBits;
  if (remaining > 7) throw new Error('Corrupt stream: trailing data after final row');
  if (remaining > 0 && reader.readBits(remaining) !== 0) {
    throw new Error('Corrupt stream: non-zero padding bits');
  }

  return { width, height, bitDepth, pixels };
}

/** Parse just the header of a compressed stream. */
export function inspect(data: Uint8Array): CodecHeader {
  if (data.length < HEADER_BYTES) throw new Error('Corrupt stream: too short for header');
  const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
  if (view.getUint32(0, false) !== MAGIC) throw new Error('Corrupt stream: bad magic');
  return {
    width: view.getUint32(4, true),
    height: view.getUint32(8, true),
    bitDepth: view.getUint8(12) as BitDepth,
  };
}

/** Pack a binary image into a 1-bit-per-pixel bitmap (MSB first, row-major). */
export function packBits(image: RasterImage): Uint8Array {
  if (image.bitDepth !== 1) throw new Error('packBits requires a binary image');
  const { width, height, pixels } = image;
  const out = new Uint8Array(Math.ceil((width * height) / 8));
  for (let i = 0; i < width * height; i++) {
    if (pixels[i] !== 0) out[i >>> 3] |= 0x80 >>> (i & 7);
  }
  return out;
}

/** Raw size in bytes of the unpacked raster (what a naive bitmap would cost). */
export function rawSizeBytes(image: RasterImage): number {
  return Math.ceil((image.width * image.height * image.bitDepth) / 8);
}
