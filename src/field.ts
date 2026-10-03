import { OverflowChain } from './overflow.ts';

/**
 * Variable-length field codec shared by keys and values.
 *
 * A field is stored inline when it fits under MAX_INLINE_FIELD_BYTES;
 * otherwise the field body past a short prefix is truncated into an
 * overflow page chain and only the prefix + chain head pointer remain
 * in the tree page.
 *
 * Inline encoding:   [flag=0:u8][length:u32le][data]
 * Overflow encoding: [flag=1:u8][totalLength:u32le][prefixLength:u32le][prefix][firstOverflowPageId:u32le]
 */
export const MAX_INLINE_FIELD_BYTES = 1024;
export const OVERFLOW_PREFIX_BYTES = 64;

export interface EncodedField {
  bytes: Buffer;
  overflow: boolean;
  overflowPageIds: number[];
}

export interface DecodedField {
  data: Buffer;
  bytesRead: number;
  overflow: boolean;
  totalLength: number;
  prefix: Buffer | null;
  firstOverflowPageId: number | null;
}

export function encodeField(chain: OverflowChain, data: Buffer): EncodedField {
  if (data.length <= MAX_INLINE_FIELD_BYTES) {
    const bytes = Buffer.alloc(5 + data.length);
    bytes.writeUInt8(0, 0);
    bytes.writeUInt32LE(data.length, 1);
    data.copy(bytes, 5);
    return { bytes, overflow: false, overflowPageIds: [] };
  }
  const prefixLength = Math.min(OVERFLOW_PREFIX_BYTES, data.length);
  const remainder = data.subarray(prefixLength);
  const { firstPageId, pageIds } = chain.write(remainder);
  const bytes = Buffer.alloc(13 + prefixLength);
  bytes.writeUInt8(1, 0);
  bytes.writeUInt32LE(data.length, 1);
  bytes.writeUInt32LE(prefixLength, 5);
  data.copy(bytes, 9, 0, prefixLength);
  bytes.writeUInt32LE(firstPageId, 9 + prefixLength);
  return { bytes, overflow: true, overflowPageIds: pageIds };
}

export function decodeField(chain: OverflowChain, buf: Buffer, offset: number): DecodedField {
  const flag = buf.readUInt8(offset);
  const totalLength = buf.readUInt32LE(offset + 1);
  if (flag === 0) {
    const data = Buffer.from(buf.subarray(offset + 5, offset + 5 + totalLength));
    return {
      data,
      bytesRead: 5 + totalLength,
      overflow: false,
      totalLength,
      prefix: null,
      firstOverflowPageId: null,
    };
  }
  if (flag !== 1) throw new Error(`unknown field flag ${flag} at offset ${offset}`);
  const prefixLength = buf.readUInt32LE(offset + 5);
  const prefix = Buffer.from(buf.subarray(offset + 9, offset + 9 + prefixLength));
  const firstOverflowPageId = buf.readUInt32LE(offset + 9 + prefixLength);
  const remainder = chain.read(firstOverflowPageId, totalLength - prefixLength);
  return {
    data: Buffer.concat([prefix, remainder]),
    bytesRead: 13 + prefixLength,
    overflow: true,
    totalLength,
    prefix,
    firstOverflowPageId,
  };
}
