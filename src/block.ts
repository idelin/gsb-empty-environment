// Block-level codec: header (de)serialization, token payload coding, CRC
// verification. Block layout is specified in FORMAT.md.

import {
  BlockError,
  DEFAULT_MAX_BLOCK_SIZE,
  END_SYMBOL,
  FIXED_HEADER_SIZE,
  MAGIC0,
  MAGIC1,
  MAX_PAYLOAD_SIZE,
  MAX_WINDOW_BITS,
  MIN_WINDOW_BITS,
  NUM_MAIN_SYMBOLS,
  VERSION,
  readU16,
  readU32,
  writeU16,
  writeU32,
} from './format.ts';
import { crc32 } from './crc32.ts';
import { BitReader, BitWriter } from './bitstream.ts';
import {
  assignCanonicalCodes,
  buildCodeLengths,
  decodeSymbol,
  makeDecoder,
  validateCodeLengths,
} from './huffman.ts';
import { lz77Tokens } from './lz77.ts';
import { distanceSlot, lengthSlot, lengthSymbol, symbolToLengthSlot } from './tokens.ts';
import type { Token } from './tokens.ts';

export interface CodecOptions {
  maxBlockSize?: number;
}

export interface ParsedHeader {
  windowBits: number;
  origLen: number;
  payloadLen: number;
  crc: number;
  mainLens: Uint8Array; // NUM_MAIN_SYMBOLS entries
  distLens: Uint8Array; // windowBits + 1 entries
  payloadOffset: number; // relative to block start
  blockBytes: number; // total block size including header, tables, payload
}

// ---------------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------------

// Encodes a token list into a payload bit stream plus the two code length
// tables. Exported so tests can craft arbitrary (including invalid) blocks.
export function encodeTokensPayload(
  tokens: Token[],
  windowBits: number,
): { payload: Uint8Array; mainLens: Uint8Array; distLens: Uint8Array } {
  const mainFreq = new Array<number>(NUM_MAIN_SYMBOLS).fill(0);
  const distFreq = new Array<number>(windowBits + 1).fill(0);
  for (const t of tokens) {
    if (t.type === 'literal') {
      mainFreq[t.byte]++;
    } else {
      mainFreq[lengthSymbol(lengthSlot(t.length))]++;
      distFreq[distanceSlot(t.distance)]++;
    }
  }
  mainFreq[END_SYMBOL]++;

  const mainLens = buildCodeLengths(mainFreq);
  const distUsed = distFreq.some((f) => f > 0);
  const distLens = distUsed ? buildCodeLengths(distFreq) : new Uint8Array(windowBits + 1);
  const mainCodes = assignCanonicalCodes(mainLens);
  const distCodes = assignCanonicalCodes(distLens);

  const w = new BitWriter();
  for (const t of tokens) {
    if (t.type === 'literal') {
      w.writeCode(mainCodes[t.byte], mainLens[t.byte]);
    } else {
      const slot = lengthSlot(t.length);
      const sym = lengthSymbol(slot);
      w.writeCode(mainCodes[sym], mainLens[sym]);
      w.writeBits(t.length - (1 << slot), slot);
      const ds = distanceSlot(t.distance);
      w.writeCode(distCodes[ds], distLens[ds]);
      w.writeBits(t.distance - (1 << ds), ds);
    }
  }
  w.writeCode(mainCodes[END_SYMBOL], mainLens[END_SYMBOL]);
  return { payload: w.finish(), mainLens, distLens };
}

// Assembles a full block from its parts. Exported for test crafting.
export function serializeBlock(
  origLen: number,
  crc: number,
  windowBits: number,
  payload: Uint8Array,
  mainLens: Uint8Array,
  distLens: Uint8Array,
): Uint8Array {
  let mainCount = mainLens.length;
  while (mainCount > 1 && mainLens[mainCount - 1] === 0) mainCount--;
  let distCount = distLens.length;
  while (distCount > 0 && distLens[distCount - 1] === 0) distCount--;

  const out = new Uint8Array(FIXED_HEADER_SIZE + mainCount + distCount + payload.length);
  out[0] = MAGIC0;
  out[1] = MAGIC1;
  out[2] = VERSION;
  out[3] = windowBits;
  writeU32(out, 4, origLen);
  writeU32(out, 8, payload.length);
  writeU32(out, 12, crc);
  writeU16(out, 16, mainCount);
  writeU16(out, 18, distCount);
  out.set(mainLens.subarray(0, mainCount), FIXED_HEADER_SIZE);
  out.set(distLens.subarray(0, distCount), FIXED_HEADER_SIZE + mainCount);
  out.set(payload, FIXED_HEADER_SIZE + mainCount + distCount);
  return out;
}

// Compresses one block (must fit the caller's block size policy).
export function encodeBlock(data: Uint8Array, windowBits: number): Uint8Array {
  const tokens = lz77Tokens(data, windowBits);
  const { payload, mainLens, distLens } = encodeTokensPayload(tokens, windowBits);
  return serializeBlock(data.length, crc32(data), windowBits, payload, mainLens, distLens);
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

export function parseHeader(buf: Uint8Array, offset: number, maxBlockSize: number): ParsedHeader {
  if (buf.length - offset < FIXED_HEADER_SIZE) {
    throw new BlockError('TRUNCATED', 'truncated block header');
  }
  if (buf[offset] !== MAGIC0 || buf[offset + 1] !== MAGIC1) {
    throw new BlockError('BAD_FORMAT', 'bad magic bytes');
  }
  if (buf[offset + 2] !== VERSION) {
    throw new BlockError('BAD_FORMAT', `unsupported version ${buf[offset + 2]}`);
  }
  const windowBits = buf[offset + 3];
  if (windowBits < MIN_WINDOW_BITS || windowBits > MAX_WINDOW_BITS) {
    throw new BlockError('BAD_FORMAT', `window bits ${windowBits} out of range`);
  }
  const origLen = readU32(buf, offset + 4);
  if (origLen > maxBlockSize) {
    throw new BlockError(
      'DECLARED_TOO_LARGE',
      `declared original length ${origLen} exceeds limit ${maxBlockSize}`,
    );
  }
  const payloadLen = readU32(buf, offset + 8);
  if (payloadLen > MAX_PAYLOAD_SIZE) {
    throw new BlockError('BAD_FORMAT', `implausible payload length ${payloadLen}`);
  }
  const crc = readU32(buf, offset + 12);
  const mainCount = readU16(buf, offset + 16);
  const distCount = readU16(buf, offset + 18);
  if (mainCount < 1 || mainCount > NUM_MAIN_SYMBOLS) {
    throw new BlockError('BAD_FORMAT', `main code length count ${mainCount} out of range`);
  }
  if (distCount > windowBits + 1) {
    throw new BlockError('BAD_FORMAT', `distance code length count ${distCount} out of range`);
  }

  const payloadOffset = FIXED_HEADER_SIZE + mainCount + distCount;
  const blockBytes = payloadOffset + payloadLen;
  if (buf.length - offset < blockBytes) {
    throw new BlockError('TRUNCATED', 'truncated block', blockBytes);
  }

  try {
    const mainLens = new Uint8Array(NUM_MAIN_SYMBOLS);
    mainLens.set(buf.subarray(offset + FIXED_HEADER_SIZE, offset + FIXED_HEADER_SIZE + mainCount));
    validateCodeLengths(mainLens, { allowEmpty: false });

    const distLens = new Uint8Array(windowBits + 1);
    distLens.set(
      buf.subarray(offset + FIXED_HEADER_SIZE + mainCount, offset + payloadOffset),
    );
    validateCodeLengths(distLens, { allowEmpty: true });

    return { windowBits, origLen, payloadLen, crc, mainLens, distLens, payloadOffset, blockBytes };
  } catch (e) {
    if (e instanceof BlockError && e.blockBytes === null) e.blockBytes = blockBytes;
    throw e;
  }
}

export interface DecodedTokens {
  tokens: Token[];
  header: ParsedHeader;
  bytesConsumed: number;
}

// Parses one block and returns its token list without expanding matches.
// Validates lengths, distances and the declared original length.
export function decodeBlockTokens(
  buf: Uint8Array,
  offset: number = 0,
  opts: CodecOptions = {},
): DecodedTokens {
  const maxBlockSize = opts.maxBlockSize ?? DEFAULT_MAX_BLOCK_SIZE;
  const header = parseHeader(buf, offset, maxBlockSize);
  const payload = buf.subarray(offset + header.payloadOffset, offset + header.blockBytes);
  const reader = new BitReader(payload);
  const mainDec = makeDecoder(header.mainLens);
  const distNonEmpty = header.distLens.some((l) => l > 0);
  const distDec = distNonEmpty ? makeDecoder(header.distLens) : null;
  const window = 1 << header.windowBits;

  const tokens: Token[] = [];
  let produced = 0;
  try {
    for (;;) {
      const sym = decodeSymbol(mainDec, reader);
      if (sym < 256) {
        if (produced >= header.origLen) {
          throw new BlockError('OVERFLOW', 'expansion exceeds declared original length');
        }
        tokens.push({ type: 'literal', byte: sym });
        produced++;
      } else if (sym < END_SYMBOL) {
        const slot = symbolToLengthSlot(sym);
        const length = (1 << slot) + reader.readBits(slot);
        if (distDec === null) {
          throw new BlockError('CORRUPT', 'match token but distance code table is empty');
        }
        const dsym = decodeSymbol(distDec, reader);
        const distance = (1 << dsym) + reader.readBits(dsym);
        if (distance > window) {
          throw new BlockError('BAD_DISTANCE', `distance ${distance} exceeds window ${window}`);
        }
        if (distance > produced) {
          throw new BlockError('BAD_DISTANCE', `distance ${distance} exceeds history ${produced}`);
        }
        if (produced + length > header.origLen) {
          throw new BlockError('OVERFLOW', 'expansion exceeds declared original length');
        }
        tokens.push({ type: 'match', length, distance });
        produced += length;
      } else {
        break; // END_SYMBOL
      }
    }
    reader.alignToByte();
    if (reader.bytePos !== payload.length) {
      throw new BlockError('CORRUPT', 'payload has trailing bytes after end symbol');
    }
    if (produced !== header.origLen) {
      throw new BlockError(
        'CORRUPT',
        `expanded length ${produced} does not match declared ${header.origLen}`,
      );
    }
  } catch (e) {
    if (e instanceof BlockError && e.blockBytes === null) e.blockBytes = header.blockBytes;
    throw e;
  }
  return { tokens, header, bytesConsumed: header.blockBytes };
}

export interface DecodedBlock {
  data: Uint8Array;
  bytesConsumed: number;
}

// Fully decodes and verifies one independent block.
export function decodeBlock(
  buf: Uint8Array,
  offset: number = 0,
  opts: CodecOptions = {},
): DecodedBlock {
  const { tokens, header, bytesConsumed } = decodeBlockTokens(buf, offset, opts);
  const out = new Uint8Array(header.origLen);
  let pos = 0;
  for (const t of tokens) {
    if (t.type === 'literal') {
      out[pos++] = t.byte;
    } else {
      // Overlapping copy semantics: the source advances byte by byte and may
      // reference bytes written by this very match (length > distance).
      let src = pos - t.distance;
      for (let k = 0; k < t.length; k++) {
        out[pos++] = out[src++];
      }
    }
  }
  if (crc32(out) !== header.crc) {
    throw new BlockError('CHECKSUM', 'crc32 mismatch', header.blockBytes);
  }
  return { data: out, bytesConsumed };
}

export interface BlockInfo {
  offset: number;
  blockBytes: number;
  origLen: number;
  payloadLen: number;
  windowBits: number;
}

// Lists block boundaries without decoding payloads (for stats/inspection).
export function inspectBlocks(buf: Uint8Array, opts: CodecOptions = {}): BlockInfo[] {
  const maxBlockSize = opts.maxBlockSize ?? DEFAULT_MAX_BLOCK_SIZE;
  const infos: BlockInfo[] = [];
  let offset = 0;
  while (offset < buf.length) {
    const h = parseHeader(buf, offset, maxBlockSize);
    infos.push({
      offset,
      blockBytes: h.blockBytes,
      origLen: h.origLen,
      payloadLen: h.payloadLen,
      windowBits: h.windowBits,
    });
    offset += h.blockBytes;
  }
  return infos;
}
