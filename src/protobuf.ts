/**
 * Hand-rolled, schema-driven Protocol Buffers wire-format parser/encoder.
 * Supports varint, zigzag, fixed32/64, length-delimited nested messages,
 * packed repeated arrays, and unknown-field skipping. All reads are
 * bounds-checked; truncation surfaces as TruncatedMessageError.
 */
import { ProtobufDecodeError } from './errors.ts';
import { assertValid, wireTypeOf, type FieldDescriptor, type MessageSchema } from './schema.ts';
import { WireReader, WireType, WireWriter } from './wire.ts';
import {
  zigZagDecode32,
  zigZagDecode64,
  zigZagEncode32,
  zigZagEncode64,
} from './varint.ts';

export type MessageValue = Record<string, unknown>;

function readScalar(reader: WireReader, type: FieldDescriptor['type']): unknown {
  switch (type) {
    case 'int32':
      return reader.readInt32();
    case 'int64':
      return reader.readInt64();
    case 'uint32':
      return reader.readUint32();
    case 'uint64':
      return reader.readUint64();
    case 'sint32':
      return reader.readSint32();
    case 'sint64':
      return reader.readSint64();
    case 'bool':
      return reader.readBool();
    case 'fixed32':
      return reader.readFixed32();
    case 'sfixed32':
      return reader.readSfixed32();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed64':
      return reader.readSfixed64();
    case 'string':
      return reader.readString();
    case 'bytes':
      return reader.readBytes();
    default:
      throw new ProtobufDecodeError(`cannot read scalar of type ${type}`);
  }
}

function expectedWireType(field: FieldDescriptor, wireType: WireType): boolean {
  if (field.packed && field.repeated) {
    // Packed scalars arrive as one length-delimited blob.
    return wireType === WireType.LengthDelimited || wireType === wireTypeOf(field);
  }
  return wireType === wireTypeOf(field);
}

/**
 * Decode `bytes` into a struct per `schema`.
 * Unknown field numbers are skipped by wire type; malformed or truncated
 * input raises ProtobufDecodeError / TruncatedMessageError.
 */
export function decodeMessage(schema: MessageSchema, bytes: Uint8Array): MessageValue {
  return decodeFromReader(schema, new WireReader(bytes));
}

function decodeFromReader(schema: MessageSchema, reader: WireReader): MessageValue {
  const byNumber = new Map(schema.fields.map((f) => [f.number, f]));
  const out: MessageValue = {};
  while (!reader.eof) {
    const tag = reader.readVarint();
    const fieldNumber = Number(tag >> 3n);
    const wireType = Number(tag & 7n) as WireType;
    if (fieldNumber === 0) {
      throw new ProtobufDecodeError('field number 0 is illegal');
    }
    const field = byNumber.get(fieldNumber);
    if (!field) {
      reader.skip(wireType);
      continue;
    }
    if (!expectedWireType(field, wireType)) {
      throw new ProtobufDecodeError(
        `field ${schema.name}.${field.name}: wire type ${wireType} does not match ` +
          `expected ${wireTypeOf(field)}`,
      );
    }
    if (field.type === 'message') {
      const payload = reader.readBytes();
      const nested = decodeFromReader(field.message!, new WireReader(payload));
      assignField(out, field, nested);
    } else if (field.packed && field.repeated && wireType === WireType.LengthDelimited) {
      const payload = reader.readBytes();
      const sub = new WireReader(payload);
      const items: unknown[] = [];
      while (!sub.eof) {
        items.push(readScalar(sub, field.type));
      }
      assignField(out, field, items);
    } else {
      assignField(out, field, readScalar(reader, field.type));
    }
  }
  return out;
}

function assignField(out: MessageValue, field: FieldDescriptor, value: unknown): void {
  if (field.repeated) {
    const existing = (out[field.name] as unknown[] | undefined) ?? [];
    if (Array.isArray(value)) {
      existing.push(...value);
    } else {
      existing.push(value);
    }
    out[field.name] = existing;
  } else {
    out[field.name] = value;
  }
}

function writeScalar(writer: WireWriter, type: FieldDescriptor['type'], value: unknown): void {
  switch (type) {
    case 'int32':
    case 'int64':
      writer.writeVarint(BigInt(value as number | bigint));
      return;
    case 'uint32':
    case 'uint64':
      writer.writeVarint(BigInt(value as number | bigint));
      return;
    case 'sint32':
      writer.writeVarint(BigInt(zigZagEncode32(value as number)));
      return;
    case 'sint64':
      writer.writeVarint(zigZagEncode64(value as bigint));
      return;
    case 'bool':
      writer.writeVarint(value ? 1n : 0n);
      return;
    case 'fixed32':
    case 'sfixed32':
      writer.writeFixed32(value as number);
      return;
    case 'fixed64':
    case 'sfixed64':
      writer.writeFixed64(value as bigint);
      return;
    case 'string':
      writer.writeString(value as string);
      return;
    case 'bytes':
      writer.writeBytes(value as Uint8Array);
      return;
    default:
      throw new ProtobufDecodeError(`cannot write scalar of type ${type}`);
  }
}

/** Encode a struct per `schema` after full reflection validation. */
export function encodeMessage(schema: MessageSchema, value: MessageValue): Uint8Array {
  assertValid(schema, value);
  const writer = new WireWriter();
  const sorted = [...schema.fields].sort((a, b) => a.number - b.number);
  for (const field of sorted) {
    const fieldValue = value[field.name];
    if (fieldValue === undefined || fieldValue === null) continue;
    if (field.repeated) {
      const items = fieldValue as unknown[];
      if (items.length === 0) continue;
      if (field.packed) {
        const packed = new WireWriter();
        for (const item of items) writeScalar(packed, field.type, item);
        writer.writeTag(field.number, WireType.LengthDelimited);
        writer.writeBytes(packed.finish());
      } else {
        for (const item of items) writeField(writer, field, item);
      }
    } else {
      writeField(writer, field, fieldValue);
    }
  }
  return writer.finish();
}

function writeField(writer: WireWriter, field: FieldDescriptor, value: unknown): void {
  if (field.type === 'message') {
    const nested = encodeMessage(field.message!, value as MessageValue);
    writer.writeTag(field.number, WireType.LengthDelimited);
    writer.writeBytes(nested);
    return;
  }
  writer.writeTag(field.number, wireTypeOf(field));
  writeScalar(writer, field.type, value);
}

export { zigZagDecode32, zigZagDecode64 };
