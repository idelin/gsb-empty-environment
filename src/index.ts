export { BitWriter } from './bitstream.ts';
export { FormatError } from './errors.ts';
export { Crc32, crc32 } from './crc32.ts';
export {
  CanonicalDecoder,
  MAX_CODE_LENGTH,
  NUM_SYMBOLS,
  buildCodeLengths,
  canonicalCodes,
  isKraftValid,
  validateCodeLengths,
} from './huffman.ts';
export {
  MAX_MATCH,
  MAX_WINDOW,
  MIN_MATCH,
  tokenize,
} from './lz77.ts';
export type { Token } from './lz77.ts';
export {
  BlockDecoder,
  FIXED_HEADER_SIZE,
  LEN_BASE,
  LEN_EXTRA,
  MAGIC,
  SYM_EOB,
  decodeBlock,
  encodeBlock,
  encodeTokensAsBlock,
  parseHeader,
} from './block.ts';
export type { BlockInfo, DecodeResult } from './block.ts';
