// Deflate decompressor (RFC 1951): handles stored, fixed-Huffman and
// dynamic-Huffman blocks.

import { BitReader } from './bitstream.ts';
import { HuffmanDecoder } from './huffman.ts';
import {
  CL_ORDER,
  DIST_BASE,
  DIST_EXTRA,
  LENGTH_BASE,
  LENGTH_EXTRA,
  fixedDistLengths,
  fixedLitlenLengths,
} from './tables.ts';

function readDynamicTables(reader: BitReader): { lit: HuffmanDecoder; dist: HuffmanDecoder } {
  const hlit = reader.readBits(5) + 257;
  const hdist = reader.readBits(5) + 1;
  const hclen = reader.readBits(4) + 4;
  const clLengths = new Array<number>(19).fill(0);
  for (let i = 0; i < hclen; i++) clLengths[CL_ORDER[i]] = reader.readBits(3);
  const clDecoder = new HuffmanDecoder(clLengths);
  const readClSymbol = () => clDecoder.decode(() => reader.readBits(1));

  const lengths: number[] = [];
  const total = hlit + hdist;
  while (lengths.length < total) {
    const sym = readClSymbol();
    if (sym <= 15) {
      lengths.push(sym);
    } else if (sym === 16) {
      if (lengths.length === 0) throw new Error('inflate: repeat with no previous length');
      const prev = lengths[lengths.length - 1];
      const count = 3 + reader.readBits(2);
      for (let i = 0; i < count; i++) lengths.push(prev);
    } else if (sym === 17) {
      const count = 3 + reader.readBits(3);
      for (let i = 0; i < count; i++) lengths.push(0);
    } else if (sym === 18) {
      const count = 11 + reader.readBits(7);
      for (let i = 0; i < count; i++) lengths.push(0);
    } else {
      throw new Error(`inflate: bad code-length symbol ${sym}`);
    }
  }
  if (lengths.length > total) throw new Error('inflate: too many code lengths');
  return {
    lit: new HuffmanDecoder(lengths.slice(0, hlit)),
    dist: new HuffmanDecoder(lengths.slice(hlit)),
  };
}

function inflateBlock(
  reader: BitReader,
  out: number[],
  lit: HuffmanDecoder,
  dist: HuffmanDecoder,
): void {
  for (;;) {
    const sym = lit.decode(() => reader.readBits(1));
    if (sym < 256) {
      out.push(sym);
    } else if (sym === 256) {
      return;
    } else {
      const li = sym - 257;
      if (li >= LENGTH_BASE.length) throw new Error(`inflate: bad length code ${sym}`);
      const length = LENGTH_BASE[li] + reader.readBits(LENGTH_EXTRA[li]);
      const dsym = dist.decode(() => reader.readBits(1));
      if (dsym >= DIST_BASE.length) throw new Error(`inflate: bad distance code ${dsym}`);
      const distance = DIST_BASE[dsym] + reader.readBits(DIST_EXTRA[dsym]);
      if (distance > out.length) throw new Error('inflate: distance too far back');
      let from = out.length - distance;
      for (let i = 0; i < length; i++) out.push(out[from++]);
    }
  }
}

export function inflate(data: Buffer | Uint8Array): Buffer {
  const reader = new BitReader(data);
  const out: number[] = [];
  for (;;) {
    const bfinal = reader.readBits(1);
    const btype = reader.readBits(2);
    if (btype === 0) {
      reader.alignToByte();
      const len = reader.readByte() | (reader.readByte() << 8);
      const nlen = reader.readByte() | (reader.readByte() << 8);
      if ((len ^ 0xffff) !== nlen) throw new Error('inflate: stored LEN/NLEN mismatch');
      const chunk = reader.copyBytes(len);
      for (const b of chunk) out.push(b);
    } else if (btype === 1) {
      inflateBlock(reader, out, new HuffmanDecoder(fixedLitlenLengths()), new HuffmanDecoder(fixedDistLengths()));
    } else if (btype === 2) {
      const { lit, dist } = readDynamicTables(reader);
      inflateBlock(reader, out, lit, dist);
    } else {
      throw new Error('inflate: reserved block type 11');
    }
    if (bfinal === 1) break;
  }
  return Buffer.from(out);
}
