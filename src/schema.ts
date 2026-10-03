/**
 * Reflection-mode schema engine: declarative field descriptors drive both
 * encoding and decoding, with structural validation on both paths.
 * Supports nested length-delimited messages and packed repeated fields.
 */

import { ProtobufDecodeError } from "./varint.ts";
import { WireReader, WireType, WireWriter } from "./wire.ts";
import type { WireTypeValue } from "./wire.ts";

export type ScalarType =
  | "uint32"
  | "uint64"
  | "int32"
  | "int64"
  | "sint32"
  | "sint64"
  | "bool"
  | "fixed32"
  | "fixed64"
  | "sfixed32"
  | "sfixed64"
  | "float"
  | "double"
  | "string"
  | "bytes";

export type FieldType = ScalarType | "message";

export interface FieldDescriptor {
  readonly name: string;
  readonly fieldNumber: number;
  readonly type: FieldType;
  readonly repeated?: boolean;
  /** Pack repeated scalars into one length-delimited field. */
  readonly packed?: boolean;
  /** Required when type === "message". */
  readonly schema?: MessageSchema;
}

export type RecordValue =
  | number
  | bigint
  | boolean
  | string
  | Uint8Array
  | MessageRecord
  | Array<number | bigint | boolean | string | Uint8Array | MessageRecord>;

export interface MessageRecord {
  [field: string]: RecordValue | undefined;
}

const WIRE_TYPE_OF: Record<ScalarType, WireTypeValue> = {
  uint32: WireType.VARINT,
  uint64: WireType.VARINT,
  int32: WireType.VARINT,
  int64: WireType.VARINT,
  sint32: WireType.VARINT,
  sint64: WireType.VARINT,
  bool: WireType.VARINT,
  fixed32: WireType.FIXED32,
  sfixed32: WireType.FIXED32,
  float: WireType.FIXED32,
  fixed64: WireType.FIXED64,
  sfixed64: WireType.FIXED64,
  double: WireType.FIXED64,
  string: WireType.LENGTH_DELIMITED,
  bytes: WireType.LENGTH_DELIMITED,
};

const PACKABLE: ReadonlySet<ScalarType> = new Set([
  "uint32",
  "uint64",
  "int32",
  "int64",
  "sint32",
  "sint64",
  "bool",
  "fixed32",
  "fixed64",
  "sfixed32",
  "sfixed64",
  "float",
  "double",
]);

export class SchemaValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SchemaValidationError";
  }
}

function fieldWireType(field: FieldDescriptor): WireTypeValue {
  if (field.type === "message") return WireType.LENGTH_DELIMITED;
  return WIRE_TYPE_OF[field.type];
}

function writeScalar(writer: WireWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case "uint32":
      writer.writeUint32(asNumber(type, value));
      return;
    case "uint64":
      writer.writeUint64(asBigint(type, value));
      return;
    case "int32":
      writer.writeInt32(asNumber(type, value));
      return;
    case "int64":
      writer.writeInt64(asBigint(type, value));
      return;
    case "sint32":
      writer.writeSint32(asNumber(type, value));
      return;
    case "sint64":
      writer.writeSint64(asBigint(type, value));
      return;
    case "bool":
      writer.writeBool(asBoolean(type, value));
      return;
    case "fixed32":
      writer.writeFixed32(asNumber(type, value));
      return;
    case "fixed64":
      writer.writeFixed64(asBigint(type, value));
      return;
    case "sfixed32":
      writer.writeSfixed32(asNumber(type, value));
      return;
    case "sfixed64":
      writer.writeSfixed64(asBigint(type, value));
      return;
    case "float":
      writer.writeFloat(asNumber(type, value));
      return;
    case "double":
      writer.writeDouble(asNumber(type, value));
      return;
    case "string":
      writer.writeString(asString(type, value));
      return;
    case "bytes":
      writer.writeBytes(asBytes(type, value));
      return;
  }
}

function readScalar(reader: WireReader, type: ScalarType): number | bigint | boolean | string | Uint8Array {
  switch (type) {
    case "uint32":
      return reader.readUint32();
    case "uint64":
      return reader.readUint64();
    case "int32":
      return reader.readInt32();
    case "int64":
      return reader.readInt64();
    case "sint32":
      return reader.readSint32();
    case "sint64":
      return reader.readSint64();
    case "bool":
      return reader.readBool();
    case "fixed32":
      return reader.readFixed32();
    case "fixed64":
      return reader.readFixed64();
    case "sfixed32":
      return reader.readSfixed32();
    case "sfixed64":
      return reader.readSfixed64();
    case "float":
      return reader.readFloat();
    case "double":
      return reader.readDouble();
    case "string":
      return reader.readString();
    case "bytes":
      return reader.readBytes().slice(); // copy: detach from parent buffer
  }
}

function asNumber(type: string, value: unknown): number {
  if (typeof value !== "number" || Number.isNaN(value)) {
    throw new SchemaValidationError(
      `field of type ${type} expects a number, got ${typeof value}`,
    );
  }
  return value;
}

function asBigint(type: string, value: unknown): bigint {
  if (typeof value === "bigint") return value;
  if (typeof value === "number" && Number.isInteger(value)) return BigInt(value);
  throw new SchemaValidationError(
    `field of type ${type} expects a bigint/integer, got ${typeof value}`,
  );
}

function asBoolean(type: string, value: unknown): boolean {
  if (typeof value !== "boolean") {
    throw new SchemaValidationError(
      `field of type ${type} expects a boolean, got ${typeof value}`,
    );
  }
  return value;
}

function asString(type: string, value: unknown): string {
  if (typeof value !== "string") {
    throw new SchemaValidationError(
      `field of type ${type} expects a string, got ${typeof value}`,
    );
  }
  return value;
}

function asBytes(type: string, value: unknown): Uint8Array {
  if (!(value instanceof Uint8Array)) {
    throw new SchemaValidationError(
      `field of type ${type} expects Uint8Array, got ${typeof value}`,
    );
  }
  return value;
}

export class MessageSchema {
  readonly name: string;
  readonly fields: readonly FieldDescriptor[];
  private readonly byNumber: Map<number, FieldDescriptor>;

  constructor(name: string, fields: readonly FieldDescriptor[]) {
    this.name = name;
    this.fields = fields;
    this.byNumber = new Map();
    const seenNames = new Set<string>();
    for (const f of fields) {
      if (this.byNumber.has(f.fieldNumber)) {
        throw new SchemaValidationError(
          `${name}: duplicate field number ${f.fieldNumber}`,
        );
      }
      if (seenNames.has(f.name)) {
        throw new SchemaValidationError(`${name}: duplicate field name ${f.name}`);
      }
      if (f.type === "message" && !f.schema) {
        throw new SchemaValidationError(
          `${name}.${f.name}: message field requires a nested schema`,
        );
      }
      if (f.packed && (f.type === "message" || !PACKABLE.has(f.type as ScalarType))) {
        throw new SchemaValidationError(
          `${name}.${f.name}: type ${f.type} cannot be packed`,
        );
      }
      if (f.packed && !f.repeated) {
        throw new SchemaValidationError(
          `${name}.${f.name}: packed requires repeated`,
        );
      }
      this.byNumber.set(f.fieldNumber, f);
      seenNames.add(f.name);
    }
  }

  /** Validate a record against the schema (reflection-mode checking). */
  validate(record: MessageRecord, path = this.name): void {
    if (typeof record !== "object" || record === null || Array.isArray(record)) {
      throw new SchemaValidationError(`${path}: expected an object record`);
    }
    for (const f of this.fields) {
      const value = record[f.name];
      if (value === undefined) continue;
      const here = `${path}.${f.name}`;
      if (f.repeated) {
        if (!Array.isArray(value)) {
          throw new SchemaValidationError(`${here}: repeated field expects an array`);
        }
        for (let i = 0; i < value.length; i++) {
          this.validateElement(f, value[i], `${here}[${i}]`);
        }
      } else {
        this.validateElement(f, value, here);
      }
    }
  }

  private validateElement(f: FieldDescriptor, value: unknown, path: string): void {
    if (f.type === "message") {
      f.schema!.validate(value as MessageRecord, path);
      return;
    }
    switch (f.type as ScalarType) {
      case "uint32":
      case "int32":
      case "sint32":
      case "fixed32":
      case "sfixed32":
      case "float":
      case "double":
        asNumber(f.type, value);
        return;
      case "uint64":
      case "int64":
      case "sint64":
      case "fixed64":
      case "sfixed64":
        asBigint(f.type, value);
        return;
      case "bool":
        asBoolean(f.type, value);
        return;
      case "string":
        asString(f.type, value);
        return;
      case "bytes":
        asBytes(f.type, value);
        return;
    }
  }

  /** Encode a record to protobuf wire bytes. Validates before writing. */
  encode(record: MessageRecord): Uint8Array {
    this.validate(record);
    const writer = new WireWriter();
    this.writeTo(writer, record);
    return writer.toBytes();
  }

  private writeTo(writer: WireWriter, record: MessageRecord): void {
    for (const f of this.fields) {
      const value = record[f.name];
      if (value === undefined) continue;
      if (f.repeated) {
        const items = value as unknown[];
        if (items.length === 0) continue;
        if (f.packed) {
          const inner = new WireWriter();
          for (const item of items) {
            writeScalar(inner, f.type as ScalarType, item);
          }
          writer.writeTag(f.fieldNumber, WireType.LENGTH_DELIMITED);
          writer.writeBytes(inner.toBytes());
        } else {
          for (const item of items) {
            this.writeSingle(writer, f, item);
          }
        }
      } else {
        this.writeSingle(writer, f, value);
      }
    }
  }

  private writeSingle(writer: WireWriter, f: FieldDescriptor, value: unknown): void {
    writer.writeTag(f.fieldNumber, fieldWireType(f));
    if (f.type === "message") {
      const inner = new WireWriter();
      f.schema!.writeTo(inner, value as MessageRecord);
      writer.writeBytes(inner.toBytes());
    } else {
      writeScalar(writer, f.type as ScalarType, value);
    }
  }

  /** Decode wire bytes into a record. Unknown fields are skipped safely. */
  decode(bytes: Uint8Array): MessageRecord {
    const reader = new WireReader(bytes);
    const record = this.readFrom(reader);
    if (!reader.eof) {
      throw new ProtobufDecodeError(
        `${this.name}: ${reader.remaining} trailing byte(s) after message`,
      );
    }
    return record;
  }

  private readFrom(reader: WireReader): MessageRecord {
    const record: MessageRecord = {};
    for (;;) {
      const tag = reader.readTag();
      if (tag === null) break;
      const field = this.byNumber.get(tag.fieldNumber);
      if (!field) {
        reader.skip(tag.wireType);
        continue;
      }
      this.readField(reader, field, tag.wireType, record);
    }
    return record;
  }

  private readField(
    reader: WireReader,
    field: FieldDescriptor,
    wireType: WireTypeValue,
    record: MessageRecord,
  ): void {
    const expected = fieldWireType(field);
    // Packed repeated scalar: one length-delimited blob of raw values.
    if (
      field.repeated &&
      field.type !== "message" &&
      PACKABLE.has(field.type as ScalarType) &&
      wireType === WireType.LENGTH_DELIMITED
    ) {
      const sub = reader.readSubreader();
      const items = (record[field.name] as unknown[]) ?? [];
      while (!sub.eof) {
        items.push(readScalar(sub, field.type as ScalarType));
      }
      record[field.name] = items as RecordValue;
      return;
    }
    if (wireType !== expected) {
      throw new ProtobufDecodeError(
        `${this.name}.${field.name}: wire type ${wireType}, expected ${expected}`,
      );
    }
    if (field.type === "message") {
      const sub = reader.readSubreader();
      const child = f_schema(field).readFrom(sub);
      if (!sub.eof) {
        throw new ProtobufDecodeError(
          `${this.name}.${field.name}: nested message has trailing bytes`,
        );
      }
      if (field.repeated) {
        const items = (record[field.name] as unknown[]) ?? [];
        items.push(child);
        record[field.name] = items as RecordValue;
      } else {
        record[field.name] = child;
      }
      return;
    }
    const value = readScalar(reader, field.type as ScalarType);
    if (field.repeated) {
      const items = (record[field.name] as unknown[]) ?? [];
      items.push(value);
      record[field.name] = items as RecordValue;
    } else {
      record[field.name] = value as RecordValue;
    }
  }
}

function f_schema(field: FieldDescriptor): MessageSchema {
  return field.schema!;
}

/** Convenience: define a schema with a fluent literal. */
export function defineMessage(
  name: string,
  fields: readonly FieldDescriptor[],
): MessageSchema {
  return new MessageSchema(name, fields);
}
