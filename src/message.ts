import { ProtobufDecodeError } from './errors.ts';
import { WireReader, WireType } from './reader.ts';
import { WireWriter } from './writer.ts';
import { zigzagEncode32, zigzagEncode64 } from './varint.ts';
import {
  expectedWireType,
  isPackable,
  resolveMessage,
  type FieldDescriptor,
  type MessageSchema,
} from './schema.ts';

export const DEFAULT_MAX_DEPTH = 64;

export interface DecodeOptions {
  maxDepth?: number;
}

export type MessageObject = Record<string, unknown>;

function writeScalar(writer: WireWriter, field: FieldDescriptor, value: unknown): void {
  switch (field.kind) {
    case 'int32':
    case 'enum':
      writer.writeVarint(BigInt(Math.trunc(value as number)));
      return;
    case 'int64':
      writer.writeVarint(value as bigint);
      return;
    case 'uint32':
      writer.writeVarint((value as number) >>> 0);
      return;
    case 'uint64':
      writer.writeVarint(BigInt.asUintN(64, value as bigint));
      return;
    case 'sint32':
      writer.writeVarint(zigzagEncode32(value as number));
      return;
    case 'sint64':
      writer.writeVarint(zigzagEncode64(value as bigint));
      return;
    case 'bool':
      writer.writeVarint(value ? 1 : 0);
      return;
    case 'fixed32':
      writer.writeFixed32(value as number);
      return;
    case 'sfixed32':
      writer.writeFixed32((value as number) | 0);
      return;
    case 'float':
      writer.writeFloat(value as number);
      return;
    case 'fixed64':
      writer.writeFixed64(value as bigint);
      return;
    case 'sfixed64':
      writer.writeFixed64(BigInt.asIntN(64, value as bigint));
      return;
    case 'double':
      writer.writeDouble(value as number);
      return;
    default:
      throw new ProtobufDecodeError('SCHEMA_MISMATCH', `kind "${field.kind}" is not a scalar`);
  }
}

function writeValue(writer: WireWriter, field: FieldDescriptor, value: unknown, depth: number): void {
  switch (field.kind) {
    case 'string': {
      if (typeof value !== 'string') {
        throw new ProtobufDecodeError('SCHEMA_MISMATCH', `field "${field.name}" expects a string`);
      }
      writer.writeLengthDelimited(new TextEncoder().encode(value));
      return;
    }
    case 'bytes': {
      if (!(value instanceof Uint8Array)) {
        throw new ProtobufDecodeError('SCHEMA_MISMATCH', `field "${field.name}" expects Uint8Array`);
      }
      writer.writeLengthDelimited(value);
      return;
    }
    case 'message': {
      const nested = resolveMessage(field.message);
      if (!nested) {
        throw new ProtobufDecodeError('SCHEMA_MISMATCH', `field "${field.name}" has no nested schema`);
      }
      const inner = new WireWriter();
      encodeInto(nested, inner, value as MessageObject, depth + 1);
      writer.writeLengthDelimited(inner.finish());
      return;
    }
    default:
      writeScalar(writer, field, value);
  }
}

function writeTagged(writer: WireWriter, field: FieldDescriptor, value: unknown, depth: number): void {
  writer.writeTag(field.number, expectedWireType(field.kind));
  writeValue(writer, field, value, depth);
}

function isPackedByDefault(field: FieldDescriptor): boolean {
  return field.packed ?? (field.repeated === true && isPackable(field.kind));
}

function encodeInto(schema: MessageSchema, writer: WireWriter, message: MessageObject, depth: number): void {
  if (depth > DEFAULT_MAX_DEPTH) {
    throw new ProtobufDecodeError('RECURSION_LIMIT', `message nesting exceeds ${DEFAULT_MAX_DEPTH}`);
  }
  for (const field of schema.fields) {
    const value = message[field.name];
    if (value === undefined || value === null) {
      if (field.required) {
        throw new ProtobufDecodeError('MISSING_REQUIRED', `required field "${field.name}" is missing`);
      }
      continue;
    }
    if (field.repeated) {
      if (!Array.isArray(value)) {
        throw new ProtobufDecodeError('SCHEMA_MISMATCH', `repeated field "${field.name}" expects an array`);
      }
      if (value.length === 0) continue;
      if (isPackedByDefault(field)) {
        const payload = new WireWriter();
        for (const element of value) writeScalar(payload, field, element);
        writer.writeTag(field.number, WireType.LengthDelimited);
        writer.writeLengthDelimited(payload.finish());
      } else {
        for (const element of value) writeTagged(writer, field, element, depth);
      }
    } else {
      writeTagged(writer, field, value, depth);
    }
  }
}

export function encodeMessage(schema: MessageSchema, message: MessageObject): Uint8Array {
  const writer = new WireWriter();
  encodeInto(schema, writer, message, 0);
  return writer.finish();
}

function readScalar(reader: WireReader, field: FieldDescriptor): unknown {
  switch (field.kind) {
    case 'int32':
    case 'enum':
      return reader.readVarint32();
    case 'int64':
      return reader.readInt64();
    case 'uint32':
      return reader.readUint32();
    case 'uint64':
      return reader.readVarint64();
    case 'sint32':
      return reader.readZigZag32();
    case 'sint64':
      return reader.readZigZag64();
    case 'bool':
      return reader.readBool();
    case 'fixed32':
      return reader.readFixed32();
    case 'sfixed32':
      return reader.readSFixed32();
    case 'float':
      return reader.readFloat();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed64':
      return reader.readSFixed64();
    case 'double':
      return reader.readDouble();
    default:
      throw new ProtobufDecodeError('SCHEMA_MISMATCH', `kind "${field.kind}" is not a scalar`);
  }
}

function readValue(reader: WireReader, field: FieldDescriptor, depth: number, maxDepth: number): unknown {
  switch (field.kind) {
    case 'string':
      return reader.readString();
    case 'bytes':
      return reader.readBytes();
    case 'message': {
      const nested = resolveMessage(field.message);
      if (!nested) {
        throw new ProtobufDecodeError('SCHEMA_MISMATCH', `field "${field.name}" has no nested schema`);
      }
      const sub = reader.fork();
      return decodeInto(nested, sub, depth + 1, maxDepth);
    }
    default:
      return readScalar(reader, field);
  }
}

function decodeInto(
  schema: MessageSchema,
  reader: WireReader,
  depth: number,
  maxDepth: number,
): MessageObject {
  if (depth > maxDepth) {
    throw new ProtobufDecodeError('RECURSION_LIMIT', `message nesting exceeds ${maxDepth}`, reader.pos);
  }
  const out: MessageObject = {};
  for (const field of schema.fields) {
    if (field.repeated) out[field.name] = [];
    else if (field.defaultValue !== undefined) out[field.name] = field.defaultValue;
  }
  while (!reader.isAtEnd) {
    const { fieldNumber, wireType } = reader.readTag();
    const field = schema.byNumber.get(fieldNumber);
    if (!field) {
      reader.skipField(wireType, fieldNumber); // forward compatibility: unknown fields are skipped
      continue;
    }
    const expected = expectedWireType(field.kind);
    if (wireType === expected) {
      const value = readValue(reader, field, depth, maxDepth);
      if (field.repeated) (out[field.name] as unknown[]).push(value);
      else out[field.name] = value;
    } else if (wireType === WireType.LengthDelimited && field.repeated && isPackable(field.kind)) {
      const sub = reader.fork(); // packed encoding, accepted regardless of the packed flag
      const target = out[field.name] as unknown[];
      while (!sub.isAtEnd) target.push(readScalar(sub, field));
    } else {
      throw new ProtobufDecodeError(
        'SCHEMA_MISMATCH',
        `field "${field.name}" (#${fieldNumber}) expects wire type ${expected}, got ${wireType}`,
        reader.pos,
      );
    }
  }
  for (const field of schema.fields) {
    if (field.required && !(field.name in out)) {
      throw new ProtobufDecodeError('MISSING_REQUIRED', `required field "${field.name}" is absent`);
    }
  }
  return out;
}

export function decodeMessage(
  schema: MessageSchema,
  data: Uint8Array | WireReader,
  options: DecodeOptions = {},
): MessageObject {
  const reader = data instanceof WireReader ? data : new WireReader(data);
  return decodeInto(schema, reader, 0, options.maxDepth ?? DEFAULT_MAX_DEPTH);
}
