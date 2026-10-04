import { WireReader, WireType, type WireTypeValue } from './reader.ts';
import { ProtobufDecodeError } from './errors.ts';

/** A single raw field decoded without any schema knowledge. */
export interface RawField {
  fieldNumber: number;
  wireType: WireTypeValue;
  /** bigint for varint/fixed64, number for fixed32, Uint8Array view for length-delimited. */
  value: bigint | number | Uint8Array;
}

/** Decode a whole message into a flat list of raw fields (schema-less). */
export function decodeRawFields(buf: Uint8Array): RawField[] {
  const reader = new WireReader(buf);
  const fields: RawField[] = [];
  while (!reader.eof) {
    const { fieldNumber, wireType } = reader.readTag();
    switch (wireType) {
      case WireType.Varint:
        fields.push({ fieldNumber, wireType, value: reader.readVarint() });
        break;
      case WireType.Fixed64:
        fields.push({ fieldNumber, wireType, value: reader.readFixed64() });
        break;
      case WireType.LengthDelimited:
        fields.push({ fieldNumber, wireType, value: reader.readBytes() });
        break;
      case WireType.Fixed32:
        fields.push({ fieldNumber, wireType, value: reader.readFixed32() });
        break;
      default:
        throw new ProtobufDecodeError(`unsupported wire type ${wireType}`, reader.offset);
    }
  }
  return fields;
}

/**
 * Decode a packed repeated varint payload (e.g. packed int32/sint64/enum).
 * The input must be the raw bytes of a single length-delimited field.
 */
export function decodePackedVarints(payload: Uint8Array): bigint[] {
  const reader = new WireReader(payload);
  const out: bigint[] = [];
  while (!reader.eof) out.push(reader.readVarint());
  return out;
}

/** Decode a packed repeated fixed32 payload. Length must be a multiple of 4. */
export function decodePackedFixed32(payload: Uint8Array): number[] {
  if (payload.length % 4 !== 0) {
    throw new ProtobufDecodeError(
      `packed fixed32 payload length ${payload.length} is not a multiple of 4`,
      0,
    );
  }
  const reader = new WireReader(payload);
  const out: number[] = [];
  while (!reader.eof) out.push(reader.readFixed32());
  return out;
}

/** Decode a packed repeated fixed64 payload. Length must be a multiple of 8. */
export function decodePackedFixed64(payload: Uint8Array): bigint[] {
  if (payload.length % 8 !== 0) {
    throw new ProtobufDecodeError(
      `packed fixed64 payload length ${payload.length} is not a multiple of 8`,
      0,
    );
  }
  const reader = new WireReader(payload);
  const out: bigint[] = [];
  while (!reader.eof) out.push(reader.readFixed64());
  return out;
}
