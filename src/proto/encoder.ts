import { WireWriter } from '../wire/writer.ts';
import { WIRE_VARINT, WIRE_I64, WIRE_LEN, WIRE_I32 } from '../wire/reader.ts';
import { zigzagEncode32, zigzagEncode64 } from '../wire/varint.ts';
import { isPackable, type FieldDescriptor, type MessageDescriptor } from './schema.ts';

function scalarWireType(type: FieldDescriptor['type']): number {
  switch (type) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
    case 'sint32': case 'sint64': case 'bool': case 'enum':
      return WIRE_VARINT;
    case 'fixed64': case 'sfixed64': case 'double':
      return WIRE_I64;
    case 'fixed32': case 'sfixed32': case 'float':
      return WIRE_I32;
    default:
      return WIRE_LEN;
  }
}

function writeScalarPayload(writer: WireWriter, type: FieldDescriptor['type'], value: unknown): void {
  switch (type) {
    case 'int32': {
      const n = value as number;
      writer.writeVarint(n < 0 ? BigInt(n) & 0xffffffffffffffffn : n);
      return;
    }
    case 'int64': writer.writeVarint(BigInt(value as bigint) & 0xffffffffffffffffn); return;
    case 'uint32': writer.writeVarint((value as number) >>> 0); return;
    case 'uint64': writer.writeVarint(BigInt(value as bigint) & 0xffffffffffffffffn); return;
    case 'sint32': writer.writeVarint(zigzagEncode32(value as number)); return;
    case 'sint64': writer.writeVarint(zigzagEncode64(BigInt(value as bigint))); return;
    case 'bool': writer.writeVarint(value ? 1 : 0); return;
    case 'enum': writer.writeVarint((value as number) | 0); return;
    case 'fixed32': writer.writeFixed32(value as number); return;
    case 'fixed64': writer.writeFixed64(BigInt(value as bigint)); return;
    case 'sfixed32': writer.writeFixed32((value as number) | 0); return;
    case 'sfixed64': writer.writeFixed64(BigInt(value as bigint)); return;
    case 'float': writer.writeFloat(value as number); return;
    case 'double': writer.writeDouble(value as number); return;
    case 'string': writer.writeString(value as string); return;
    case 'bytes': writer.writeLengthDelimited(value as Uint8Array); return;
    default: throw new Error(`cannot encode scalar of type ${type}`);
  }
}

function writeField(writer: WireWriter, field: FieldDescriptor, value: unknown): void {
  if (field.type === 'message') {
    if (!field.message) throw new Error(`field "${field.name}" missing message descriptor`);
    const inner = new WireWriter();
    encodeMessageInto(field.message, value as Record<string, unknown>, inner);
    writer.writeTag(field.no, WIRE_LEN);
    writer.writeLengthDelimited(inner.finish());
    return;
  }
  const wire = scalarWireType(field.type);
  writer.writeTag(field.no, wire);
  writeScalarPayload(writer, field.type, value);
}

function writePacked(writer: WireWriter, field: FieldDescriptor, values: unknown[]): void {
  const inner = new WireWriter();
  for (const v of values) writeScalarPayload(inner, field.type, v);
  writer.writeTag(field.no, WIRE_LEN);
  writer.writeLengthDelimited(inner.finish());
}

function encodeMessageInto(desc: MessageDescriptor, obj: Record<string, unknown>, writer: WireWriter): void {
  for (const field of desc.fields) {
    const value = obj[field.name];
    if (value === undefined || value === null) continue;
    if (field.repeated) {
      const values = value as unknown[];
      if (values.length === 0) continue;
      if (field.packed && isPackable(field.type)) {
        writePacked(writer, field, values);
      } else {
        for (const v of values) writeField(writer, field, v);
      }
    } else {
      writeField(writer, field, value);
    }
  }
}

export function encodeMessage(desc: MessageDescriptor, obj: Record<string, unknown>): Uint8Array {
  const writer = new WireWriter();
  encodeMessageInto(desc, obj, writer);
  return writer.finish();
}
