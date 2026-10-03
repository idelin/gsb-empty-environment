/**
 * Reflection-style message codec: validates and (de)serializes plain objects
 * against a declarative schema, on top of the raw wire primitives.
 */

import {
  MalformedWireError,
  TruncationError,
  WIRE_FIXED32,
  WIRE_FIXED64,
  WIRE_LENGTH_DELIMITED,
  WIRE_VARINT,
  WireReader,
  WireWriter,
  zigZagEncode,
  zigZagDecode,
} from './wire.ts';

export type ScalarType =
  | 'uint32'
  | 'uint64'
  | 'int32'
  | 'int64'
  | 'sint32'
  | 'sint64'
  | 'bool'
  | 'fixed32'
  | 'fixed64'
  | 'sfixed32'
  | 'sfixed64'
  | 'float'
  | 'double'
  | 'string'
  | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldSpec {
  readonly name: string;
  readonly type: FieldType;
  readonly repeated?: boolean;
  /** Pack scalar repeated fields into a single length-delimited run. */
  readonly packed?: boolean;
  /** Required when `type === 'message'`. */
  readonly message?: MessageSchema;
}

export interface MessageSchema {
  readonly name: string;
  /** Keyed by protobuf field number. */
  readonly fields: Readonly<Record<number, FieldSpec>>;
}

export type MessageValue = Record<string, unknown>;

const SCALAR_WIRE_TYPE: Record<ScalarType, 0 | 1 | 2 | 5> = {
  uint32: WIRE_VARINT,
  uint64: WIRE_VARINT,
  int32: WIRE_VARINT,
  int64: WIRE_VARINT,
  sint32: WIRE_VARINT,
  sint64: WIRE_VARINT,
  bool: WIRE_VARINT,
  fixed64: WIRE_FIXED64,
  sfixed64: WIRE_FIXED64,
  double: WIRE_FIXED64,
  string: WIRE_LENGTH_DELIMITED,
  bytes: WIRE_LENGTH_DELIMITED,
  fixed32: WIRE_FIXED32,
  sfixed32: WIRE_FIXED32,
  float: WIRE_FIXED32,
};

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

function isPackable(type: FieldType): boolean {
  return type !== 'message' && type !== 'string' && type !== 'bytes';
}

/** Validate a schema definition up front so bad descriptors fail fast. */
export function validateSchema(schema: MessageSchema): void {
  if (!schema.name) throw new MalformedWireError('schema requires a name');
  for (const [key, field] of Object.entries(schema.fields)) {
    const fieldNumber = Number(key);
    if (!Number.isInteger(fieldNumber) || fieldNumber <= 0) {
      throw new MalformedWireError(`${schema.name}: invalid field number "${key}"`);
    }
    if (!field.name) {
      throw new MalformedWireError(`${schema.name}#${fieldNumber}: field requires a name`);
    }
    if (field.type === 'message') {
      if (!field.message) {
        throw new MalformedWireError(`${schema.name}.${field.name}: message field missing nested schema`);
      }
      if (field.packed) {
        throw new MalformedWireError(`${schema.name}.${field.name}: messages cannot be packed`);
      }
      validateSchema(field.message);
    } else if (!(field.type in SCALAR_WIRE_TYPE)) {
      throw new MalformedWireError(`${schema.name}.${field.name}: unknown field type "${field.type}"`);
    }
    if (field.packed && !field.repeated) {
      throw new MalformedWireError(`${schema.name}.${field.name}: packed requires repeated`);
    }
    if (field.packed && !isPackable(field.type)) {
      throw new MalformedWireError(`${schema.name}.${field.name}: type ${field.type} is not packable`);
    }
  }
}

function readScalar(reader: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'uint32':
      return Number(BigInt.asUintN(32, reader.readVarint(type)));
    case 'uint64':
      return reader.readVarint(type);
    case 'int32':
      return Number(BigInt.asIntN(32, reader.readVarint(type)));
    case 'int64':
      return BigInt.asIntN(64, reader.readVarint(type));
    case 'sint32':
      return Number(BigInt.asIntN(32, zigZagDecode(reader.readVarint(type))));
    case 'sint64':
      return BigInt.asIntN(64, zigZagDecode(reader.readVarint(type)));
    case 'bool':
      return reader.readVarint(type) !== 0n;
    case 'fixed32':
      return reader.readFixed32(type);
    case 'sfixed32':
      return reader.readSFixed32(type);
    case 'fixed64':
      return reader.readFixed64(type);
    case 'sfixed64':
      return reader.readSFixed64(type);
    case 'float':
      return reader.readFloat(type);
    case 'double':
      return reader.readDouble(type);
    case 'string':
      return textDecoder.decode(reader.readBytes(type));
    case 'bytes':
      return reader.readBytes(type);
  }
}

function writeScalar(writer: WireWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case 'uint32':
    case 'uint64':
      writer.writeVarint(BigInt.asUintN(64, BigInt(value as bigint | number)));
      return;
    case 'int32':
    case 'int64':
      writer.writeVarint(BigInt(value as bigint | number));
      return;
    case 'sint32':
    case 'sint64':
      writer.writeVarint(zigZagEncode(BigInt(value as bigint | number)));
      return;
    case 'bool':
      writer.writeVarint(value ? 1 : 0);
      return;
    case 'fixed32':
    case 'sfixed32':
      writer.writeFixed32(Number(value));
      return;
    case 'fixed64':
    case 'sfixed64':
      writer.writeFixed64(BigInt(value as bigint | number));
      return;
    case 'float':
      writer.writeFloat(Number(value));
      return;
    case 'double':
      writer.writeDouble(Number(value));
      return;
    case 'string':
      writer.writeBytes(textEncoder.encode(String(value)));
      return;
    case 'bytes':
      writer.writeBytes(value as Uint8Array);
      return;
  }
}

function fieldWireType(field: FieldSpec): 0 | 1 | 2 | 5 {
  return field.type === 'message' ? WIRE_LENGTH_DELIMITED : SCALAR_WIRE_TYPE[field.type];
}

function writeField(writer: WireWriter, fieldNumber: number, field: FieldSpec, value: unknown): void {
  if (field.type === 'message') {
    const payload = encodeMessage(field.message as MessageSchema, value as MessageValue);
    writer.writeTag(fieldNumber, WIRE_LENGTH_DELIMITED);
    writer.writeBytes(payload);
    return;
  }
  writer.writeTag(fieldNumber, SCALAR_WIRE_TYPE[field.type as ScalarType]);
  writeScalar(writer, field.type as ScalarType, value);
}

/** Serialize a plain object into protobuf wire bytes according to the schema. */
export function encodeMessage(schema: MessageSchema, record: MessageValue): Uint8Array {
  validateSchema(schema);
  const writer = new WireWriter();
  for (const [key, field] of Object.entries(schema.fields)) {
    const fieldNumber = Number(key);
    const value = record[field.name];
    if (value === undefined || value === null) continue;
    if (field.repeated) {
      const items = value as readonly unknown[];
      if (!Array.isArray(items)) {
        throw new MalformedWireError(`${schema.name}.${field.name}: repeated field must be an array`);
      }
      if (items.length === 0) continue;
      if (field.packed) {
        const packed = new WireWriter();
        for (const item of items) writeScalar(packed, field.type as ScalarType, item);
        writer.writeTag(fieldNumber, WIRE_LENGTH_DELIMITED);
        writer.writeBytes(packed.finish());
      } else {
        for (const item of items) writeField(writer, fieldNumber, field, item);
      }
    } else {
      writeField(writer, fieldNumber, field, value);
    }
  }
  return writer.finish();
}

function assignField(out: MessageValue, field: FieldSpec, value: unknown): void {
  if (field.repeated) {
    let list = out[field.name] as unknown[] | undefined;
    if (list === undefined) {
      list = [];
      out[field.name] = list;
    }
    list.push(value);
  } else {
    out[field.name] = value;
  }
}

/** Parse protobuf wire bytes back into a plain object, validating every field. */
export function decodeMessage(schema: MessageSchema, buf: Uint8Array): MessageValue {
  validateSchema(schema);
  const reader = new WireReader(buf);
  const out: MessageValue = {};
  while (!reader.eof) {
    const tagOffset = reader.pos;
    const tag = reader.readVarint(`${schema.name} tag`);
    const fieldNumber = Number(tag >> 3n);
    const wireType = Number(tag & 7n);
    if (fieldNumber === 0) {
      throw new MalformedWireError(`${schema.name}: invalid field number 0 at offset ${tagOffset}`);
    }
    const field = schema.fields[fieldNumber];
    if (!field) {
      reader.skip(wireType);
      continue;
    }
    const context = `${schema.name}.${field.name}`;
    try {
      if (field.type === 'message') {
        if (wireType !== WIRE_LENGTH_DELIMITED) {
          throw new MalformedWireError(
            `${context}: expected wire type 2 (length-delimited), got ${wireType}`,
          );
        }
        assignField(out, field, decodeMessage(field.message as MessageSchema, reader.readBytes(context)));
      } else if (field.repeated && isPackable(field.type) && wireType === WIRE_LENGTH_DELIMITED) {
        // Packed encoding is always accepted for packable repeated scalars,
        // even when the schema declares the field unpacked (proto3 interop).
        const packed = new WireReader(reader.readBytes(`${context} (packed)`));
        while (!packed.eof) {
          assignField(out, field, readScalar(packed, field.type as ScalarType));
        }
      } else {
        const expected = fieldWireType(field);
        if (wireType !== expected) {
          throw new MalformedWireError(`${context}: expected wire type ${expected}, got ${wireType}`);
        }
        assignField(out, field, readScalar(reader, field.type as ScalarType));
      }
    } catch (error) {
      if (error instanceof TruncationError) {
        throw new TruncationError(
          error.offset,
          error.needed,
          error.available,
          error.context.startsWith(context) ? error.context : `${context}: ${error.context}`,
        );
      }
      throw error;
    }
  }
  for (const field of Object.values(schema.fields)) {
    if (field.repeated && out[field.name] === undefined) out[field.name] = [];
  }
  return out;
}
