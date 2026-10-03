import { CodecError } from './errors.ts';
import type { ByteReader } from './reader.ts';
import type { ByteWriter } from './writer.ts';

/** Protobuf-style wire types enabling forward-compatible field skipping. */
export const WIRE_VARINT = 0;
export const WIRE_FIXED64 = 1;
export const WIRE_LEN = 2;
export const WIRE_FIXED32 = 5;

export function writeFieldHeader(w: ByteWriter, tag: number, wire: number): void {
  w.varint(tag * 8 + wire);
}

export function readFieldHeader(r: ByteReader): { tag: number; wire: number } {
  const h = r.varint();
  const tag = Math.floor(h / 8);
  const wire = h % 8;
  if (tag <= 0) throw new CodecError('MALFORMED', `invalid field tag ${tag}`);
  return { tag, wire };
}

/** Skip a field of unknown tag based on its wire type (forward compat). */
export function skipField(r: ByteReader, wire: number): void {
  switch (wire) {
    case WIRE_VARINT:
      r.varint();
      return;
    case WIRE_FIXED64:
      r.skip(8);
      return;
    case WIRE_LEN: {
      const n = r.varint();
      r.skip(n);
      return;
    }
    case WIRE_FIXED32:
      r.skip(4);
      return;
    default:
      throw new CodecError('BAD_WIRE_TYPE', `unknown wire type ${wire}`);
  }
}
