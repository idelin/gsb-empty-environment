import { DecodeError } from './errors.ts';
import { isPackable, validateMessage, wireTypeOf } from './schema.ts';
import type {
  FieldDescriptor, MessageSchema, MessageValue, ScalarType,
} from './schema.ts';
import { zigzagDecode, zigzagEncode } from './varint.ts';
import { WireReader, WireType, WireWriter } from './wire.ts';

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

/* ---------------------------------- encode --------------------------------- */

function writeScalar(w: WireWriter, type: ScalarType, v: unknown): void {
  switch (type) {
    case 'int32': case 'int64':
      w.writeVarint(typeof v === 'number' ? v : v as bigint); // negatives -> 10-byte two's complement
      return;
    case 'uint32': case 'uint64':
      w.writeVarint(typeof v === 'number' ? v : v as bigint);
      return;
    case 'sint32': case 'sint64':
      w.writeVarint(zigzagEncode(v as number | bigint));
      return;
    case 'bool':
      w.writeVarint(v ? 1 : 0);
      return;
    case 'fixed32': w.writeFixed32(v as number); return;
    case 'sfixed32': w.writeSFixed32(v as number); return;
    case 'fixed64': w.writeFixed64(v as bigint); return;
    case 'sfixed64': w.writeSFixed64(v as bigint); return;
    case 'float': w.writeFloat(v as number); return;
    case 'double': w.writeDouble(v as number); return;
    case 'string': w.writeBytes(textEncoder.encode(v as string)); return;
    case 'bytes': w.writeBytes(v as Uint8Array); return;
  }
}

function writePacked(w: WireWriter, field: FieldDescriptor, values: unknown[]): void {
  const inner = new WireWriter();
  for (const v of values) writeScalar(inner, field.type as ScalarType, v);
  w.writeBytes(inner.finish());
}

function encodeField(w: WireWriter, field: FieldDescriptor, value: unknown): void {
  if (field.repeated) {
    const arr = value as unknown[];
    if (arr.length === 0) return;
    if (field.packed && isPackable(field.type)) {
      w.writeTag(field.number, WireType.LengthDelimited);
      writePacked(w, field, arr);
      return;
    }
    for (const v of arr) encodeSingle(w, field, v);
    return;
  }
  encodeSingle(w, field, value);
}

function encodeSingle(w: WireWriter, field: FieldDescriptor, v: unknown): void {
  if (field.type === 'message') {
    w.writeTag(field.number, WireType.LengthDelimited);
    w.writeBytes(encodeMessage(field.message!, v as MessageValue));
    return;
  }
  w.writeTag(field.number, wireTypeOf(field));
  writeScalar(w, field.type as ScalarType, v);
}

/** Encode a validated plain-object value into protobuf wire format. */
export function encodeMessage(schema: MessageSchema, value: MessageValue): Uint8Array {
  validateMessage(schema, value);
  const w = new WireWriter();
  for (const field of schema.fields) {
    const v = value[field.name];
    if (v === undefined) continue;
    encodeField(w, field, v);
  }
  return w.finish();
}

/* ---------------------------------- decode --------------------------------- */

function readScalar(r: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'int32': return Number(BigInt.asIntN(32, r.readVarint()));
    case 'int64': return BigInt.asIntN(64, r.readVarint());
    case 'uint32': return Number(BigInt.asUintN(32, r.readVarint()));
    case 'uint64': return r.readVarint();
    case 'sint32': return Number(BigInt.asIntN(32, zigzagDecode(r.readVarint())));
    case 'sint64': return zigzagDecode(r.readVarint());
    case 'bool': return r.readVarint() !== 0n;
    case 'fixed32': return r.readFixed32();
    case 'sfixed32': return r.readSFixed32();
    case 'fixed64': return r.readFixed64();
    case 'sfixed64': return r.readSFixed64();
    case 'float': return r.readFloat();
    case 'double': return r.readDouble();
    case 'string': return textDecoder.decode(r.readBytes());
    case 'bytes': return r.readBytes();
  }
}

function readPacked(r: WireReader, type: ScalarType, out: unknown[]): void {
  const sub = r.readSubReader();
  while (!sub.eof) out.push(readScalar(sub, type));
}

export interface DecodeOptions {
  /** Collect unknown fields as raw bytes keyed by field number. Default: silently skip. */
  retainUnknownFields?: boolean;
}

export interface DecodedMessage {
  value: MessageValue;
  unknownFields?: Map<number, Uint8Array[]>;
}

/**
 * Hand-rolled protobuf wire-format parser driven by a reflection schema.
 * Handles nested length-delimited messages and packed repeated fields.
 * Every read is bounds-checked: truncated input throws DecodeError, never reads OOB.
 */
export function decodeMessage(
  schema: MessageSchema,
  buf: Uint8Array,
  options: DecodeOptions = {},
): DecodedMessage {
  return decodeMessageFromReader(schema, new WireReader(buf), options);
}

/** Decode from an existing reader (used for bounded nested sub-messages). */
export function decodeMessageFromReader(
  schema: MessageSchema,
  reader: WireReader,
  options: DecodeOptions = {},
): DecodedMessage {
  const byNumber = new Map(schema.fields.map((f) => [f.number, f]));
  const value: MessageValue = {};
  const unknownFields = options.retainUnknownFields ? new Map<number, Uint8Array[]>() : undefined;

  while (!reader.eof) {
    const tag = reader.readTag();
    const field = byNumber.get(tag.fieldNumber);
    if (!field) {
      // Unknown field: skip safely according to its wire type.
      if (unknownFields) {
        const start = reader.offset;
        reader.skip(tag.wireType);
        const raw = reader.slice(start, reader.offset);
        const list = unknownFields.get(tag.fieldNumber) ?? [];
        list.push(raw);
        unknownFields.set(tag.fieldNumber, list);
      } else {
        reader.skip(tag.wireType);
      }
      continue;
    }
    readFieldValue(reader, field, tag.wireType, value);
  }
  return { value, unknownFields };
}

function readFieldValue(
  r: WireReader,
  field: FieldDescriptor,
  wireType: WireType,
  value: MessageValue,
): void {
  const expected = wireTypeOf(field);

  if (field.repeated) {
    const arr = (value[field.name] as unknown[] | undefined) ?? [];
    value[field.name] = arr;
    // Packed encoding arrives as length-delimited regardless of the element wire type.
    if (wireType === WireType.LengthDelimited && isPackable(field.type)) {
      readPacked(r, field.type as ScalarType, arr);
      return;
    }
    if (wireType !== expected) {
      throw new DecodeError(
        `field "${field.name}": wire type ${wireType}, expected ${expected}`,
        r.offset,
      );
    }
    arr.push(readSingle(r, field));
    return;
  }

  if (wireType !== expected) {
    throw new DecodeError(
      `field "${field.name}": wire type ${wireType}, expected ${expected}`,
      r.offset,
    );
  }
  value[field.name] = readSingle(r, field);
}

function readSingle(r: WireReader, field: FieldDescriptor): unknown {
  if (field.type === 'message') {
    const sub = r.readSubReader();
    const nested = decodeMessageFromReader(field.message!, sub);
    return nested.value;
  }
  return readScalar(r, field.type as ScalarType);
}
