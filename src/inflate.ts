/**
 * RFC 1951 decoder supporting all three block formats:
 *   0 - stored (uncompressed)
 *   1 - fixed (static) Huffman
 *   2 - dynamic Huffman
 */

import { BitReader } from "./bitstream.ts";
import { HuffmanDecoder } from "./huffman.ts";
import { fixedDistLengths, fixedLitLenLengths } from "./deflate.ts";
import {
  CLCODE_ORDER,
  DIST_BASE,
  DIST_EXTRA,
  END_OF_BLOCK,
  LENGTH_BASE,
  LENGTH_EXTRA,
} from "./tables.ts";

function readCodeLengths(
  reader: BitReader,
  clDecoder: HuffmanDecoder,
  count: number,
): number[] {
  const lengths = new Array<number>(count).fill(0);
  let i = 0;
  while (i < count) {
    const sym = clDecoder.decode(() => reader.readBit());
    if (sym < 16) {
      lengths[i++] = sym;
    } else if (sym === 16) {
      if (i === 0) throw new Error("inflate: repeat with no previous length");
      const prev = lengths[i - 1];
      const repeat = 3 + reader.readBits(2);
      if (i + repeat > count) throw new Error("inflate: code length overflow");
      for (let k = 0; k < repeat; k++) lengths[i++] = prev;
    } else if (sym === 17) {
      const repeat = 3 + reader.readBits(3);
      if (i + repeat > count) throw new Error("inflate: code length overflow");
      i += repeat; // zeros already
    } else if (sym === 18) {
      const repeat = 11 + reader.readBits(7);
      if (i + repeat > count) throw new Error("inflate: code length overflow");
      i += repeat;
    } else {
      throw new Error(`inflate: invalid code-length symbol ${sym}`);
    }
  }
  return lengths;
}

function inflateBlockData(
  reader: BitReader,
  out: number[],
  litLen: HuffmanDecoder,
  dist: HuffmanDecoder,
): void {
  for (;;) {
    const sym = litLen.decode(() => reader.readBit());
    if (sym < 256) {
      out.push(sym);
    } else if (sym === END_OF_BLOCK) {
      return;
    } else {
      const li = sym - 257;
      if (li >= LENGTH_BASE.length) {
        throw new Error(`inflate: invalid length code ${sym}`);
      }
      const length = LENGTH_BASE[li] + reader.readBits(LENGTH_EXTRA[li]);
      if (dist.empty) throw new Error("inflate: match without distance table");
      const dsym = dist.decode(() => reader.readBit());
      if (dsym >= DIST_BASE.length) {
        throw new Error(`inflate: invalid distance code ${dsym}`);
      }
      const distance = DIST_BASE[dsym] + reader.readBits(DIST_EXTRA[dsym]);
      if (distance > out.length) {
        throw new Error("inflate: distance too far back");
      }
      let from = out.length - distance;
      for (let k = 0; k < length; k++) out.push(out[from++]);
    }
  }
}

/** Decompress a raw Deflate stream. */
export function inflate(input: Uint8Array): Uint8Array {
  const reader = new BitReader(input);
  const out: number[] = [];

  for (;;) {
    const bfinal = reader.readBits(1);
    const btype = reader.readBits(2);

    if (btype === 0) {
      // Stored: byte-aligned LEN/NLEN header followed by raw bytes.
      reader.alignToByte();
      const len = reader.readByte() | (reader.readByte() << 8);
      const nlen = reader.readByte() | (reader.readByte() << 8);
      if ((len ^ 0xffff) !== nlen) {
        throw new Error("inflate: stored block LEN/NLEN mismatch");
      }
      for (const b of reader.readBytes(len)) out.push(b);
    } else if (btype === 1) {
      inflateBlockData(
        reader,
        out,
        new HuffmanDecoder(fixedLitLenLengths()),
        new HuffmanDecoder(fixedDistLengths()),
      );
    } else if (btype === 2) {
      const hlit = 257 + reader.readBits(5);
      const hdist = 1 + reader.readBits(5);
      const hclen = 4 + reader.readBits(4);
      if (hlit > 286 || hdist > 30) {
        throw new Error("inflate: too many lit/dist codes");
      }
      const clLengths = new Array<number>(19).fill(0);
      for (let i = 0; i < hclen; i++) {
        clLengths[CLCODE_ORDER[i]] = reader.readBits(3);
      }
      const clDecoder = new HuffmanDecoder(clLengths);
      const all = readCodeLengths(reader, clDecoder, hlit + hdist);
      const litLen = new HuffmanDecoder(all.slice(0, hlit));
      const dist = new HuffmanDecoder(all.slice(hlit));
      inflateBlockData(reader, out, litLen, dist);
    } else {
      throw new Error("inflate: reserved block type 3");
    }

    if (bfinal === 1) break;
  }

  return Uint8Array.from(out);
}
