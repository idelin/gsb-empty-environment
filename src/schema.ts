import { DecodeError, ValidationError } from "./errors.ts";
import {
  WIRE_FIXED32,
  WIRE_FIXED64,
  WIRE_LENGTH_DELIMITED,
  WIRE_VARINT,
  WireReader,
} from "./reader.ts";
import { WireWriter } from "./writer.ts";
import { MASK32, MASK64, toSigned32, toSigned64, zigzagEncode } from "./varint.ts";

export type ScalarType =
  | "uint32"
  | "uint64"
  | "int32"
  | "int64"
  | "sint32"
  | "sint64"
  | "bool"
  | "fixed32"
  | "sfixed32"
  | "fixed64"
  | "sfixed64"
  | "bytes"
  | "string";

export type FieldType = ScalarType | "message";

export interface FieldSpec {
  id: number;
  name: string;
  type: FieldType;
  repeated?: boolean;
  /** Repeated numeric fields are written packed (proto3 style) unless false. */
  packed?: boolean;
  /** Required when type === "message". */
  schema?: MessageSchema;
}

export interface MessageSchema {
  name: string;
  fields: FieldSpec[];
}

export type MessageObject = Record<string, unknown>;

const WIRE_TYPE_OF: Record<ScalarType, number> = {
  uint32: WIRE_VARINT,
  uint64: WIRE_VARINT,
  int32: WIRE_VARINT,
  int64: WIRE_VARINT,
  sint32: WIRE_VARINT,
  sint64: WIRE_VARINT,
  bool: WIRE_VARINT,
  fixed32: WIRE_FIXED32,
  sfixed32: WIRE_FIXED32,
  fixed64: WIRE_FIXED64,
  sfixed64: WIRE_FIXED64,
  bytes: WIRE_LENGTH_DELIMITED,
  string: WIRE_LENGTH_DELIMITED,
};

function isPackable(type: FieldType): boolean {
  return type !== "message" && WIRE_TYPE_OF[type as ScalarType] !== WIRE_LENGTH_DELIMITED;
}

function readScalar(reader: WireReader, type: ScalarType): unknown {
  switch (type) {
    case "uint32":
      return Number(reader.readVarint() & MASK32);
    case "uint64":
      return reader.readVarint();
    case "int32":
      return toSigned32(Number(reader.readVarint() & MASK32));
    case "int64":
      return toSigned64(reader.readVarint());
    case "sint32":
      return toSigned32(Number(reader.readZigZag() & MASK32));
    case "sint64":
      return reader.readZigZag();
    case "bool":
      return reader.readBool();
    case "fixed32":
      return reader.readFixed32();
    case "sfixed32":
      return reader.readSFixed32();
    case "fixed64":
      return reader.readFixed64();
    case "sfixed64":
      return reader.readSFixed64();
    case "bytes":
      return reader.readBytes();
    case "string":
      return reader.readString();
  }
}

function writeScalar(writer: WireWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case "uint32":
    case "int32":
      writer.writeVarint(Number(value) >>> 0);
      return;
    case "uint64":
    case "int64":
      writer.writeVarint(BigInt(value as bigint));
      return;
    case "sint32":
      writer.writeVarint(zigzagEncode(BigInt(Number(value))));
      return;
    case "sint64":
      writer.writeVarint(zigzagEncode(BigInt(value as bigint)));
      return;
    case "bool":
      writer.writeVarint(value ? 1 : 0);
      return;
    case "fixed32":
      writer.writeFixed32(Number(value));
      return;
    case "sfixed32":
      writer.writeFixed32(Number(value));
      return;
    case "fixed64":
    case "sfixed64":
      writer.writeFixed64(BigInt(value as bigint));
      return;
    case "bytes":
      writer.writeBytes(value as Uint8Array);
      return;
    case "string":
      writer.writeString(String(value));
      return;
  }
}

function fieldById(schema: MessageSchema): Map<number, FieldSpec> {
  const map = new Map<number, FieldSpec>();
  for (const f of schema.fields) map.set(f.id, f);
  return map;
}

/**
 * Reflection-mode decode: walk the wire stream, resolve each tag against the
 * schema, and rebuild a plain object. Unknown fields are skipped safely;
 * truncated input raises DecodeError carrying the dotted field path.
 */
export function decodeMessage(
  schema: MessageSchema,
  input: Uint8Array | WireReader,
  path = schema.name,
): MessageObject {
  const reader = input instanceof WireReader ? input : new WireReader(input);
  const byId = fieldById(schema);
  const out: MessageObject = {};

  while (!reader.eof) {
    const tag = reader.readTag();
    if (tag === null) break;
    const spec = byId.get(tag.field);
    if (spec === undefined) {
      reader.skip(tag.wireType);
      continue;
    }
    const fieldPath = `${path}.${spec.name}`;

    if (spec.type === "message") {
      if (tag.wireType !== WIRE_LENGTH_DELIMITED) {
        throw new DecodeError(`wire type mismatch for message field (got ${tag.wireType})`, reader.pos, fieldPath);
      }
      const child = reader.subReader();
      const value = decodeMessage(spec.schema as MessageSchema, child, fieldPath);
      if (!child.eof) {
        throw new DecodeError("nested message has trailing bytes", child.pos, fieldPath);
      }
      if (spec.repeated) {
        (out[spec.name] ??= []).push(value);
      } else {
        out[spec.name] = value;
      }
      continue;
    }

    const scalar = spec.type as ScalarType;
    const expected = WIRE_TYPE_OF[scalar];

    if (spec.repeated && isPackable(scalar) && tag.wireType === WIRE_LENGTH_DELIMITED) {
      // Packed repeated: a length-delimited block of back-to-back values.
      const block = reader.subReader();
      const list = (out[spec.name] ??= []) as unknown[];
      while (!block.eof) {
        list.push(readScalar(block, scalar));
      }
      continue;
    }

    if (tag.wireType !== expected) {
      throw new DecodeError(
        `wire type mismatch: expected ${expected}, got ${tag.wireType}`,
        reader.pos,
        fieldPath,
      );
    }
    const value = readScalar(reader, scalar);
    if (spec.repeated) {
      (out[spec.name] ??= []).push(value);
    } else {
      out[spec.name] = value;
    }
  }
  return out;
}

/** Reflection-mode encode: serialize a plain object according to the schema. */
export function encodeMessage(schema: MessageSchema, obj: MessageObject): Uint8Array {
  const writer = new WireWriter();
  encodeInto(schema, obj, writer);
  return writer.finish();
}

function encodeInto(schema: MessageSchema, obj: MessageObject, writer: WireWriter): void {
  for (const spec of schema.fields) {
    const value = obj[spec.name];
    if (value === undefined || value === null) continue;

    if (spec.repeated) {
      const list = value as unknown[];
      if (list.length === 0) continue;
      if (spec.type !== "message" && spec.packed !== false && isPackable(spec.type)) {
        const scalar = spec.type as ScalarType;
        writer.writeTag(spec.id, WIRE_LENGTH_DELIMITED);
        const block = new WireWriter();
        for (const item of list) writeScalar(block, scalar, item);
        writer.writeBytes(block.finish());
      } else {
        for (const item of list) encodeSingle(spec, item, writer);
      }
      continue;
    }
    encodeSingle(spec, value, writer);
  }
}

function encodeSingle(spec: FieldSpec, value: unknown, writer: WireWriter): void {
  if (spec.type === "message") {
    writer.writeTag(spec.id, WIRE_LENGTH_DELIMITED);
    writer.writeBytes(encodeMessage(spec.schema as MessageSchema, value as MessageObject));
    return;
  }
  const scalar = spec.type as ScalarType;
  writer.writeTag(spec.id, WIRE_TYPE_OF[scalar]);
  writeScalar(writer, scalar, value);
}

const INT_RANGES: Partial<Record<ScalarType, [bigint, bigint]>> = {
  uint32: [0n, BigInt(MASK32)],
  int32: [-0x8000_0000n, 0x7fff_ffffn],
  sint32: [-0x8000_0000n, 0x7fff_ffffn],
  sfixed32: [-0x8000_0000n, 0x7fff_ffffn],
  fixed32: [0n, BigInt(MASK32)],
  uint64: [0n, MASK64],
  int64: [-(1n << 63n), MASK64 >> 1n],
  sint64: [-(1n << 63n), MASK64 >> 1n],
  fixed64: [0n, MASK64],
  sfixed64: [-(1n << 63n), MASK64 >> 1n],
};

/**
 * Reflection-mode validation engine: verify a plain object satisfies the
 * schema (types, integral ranges, repeated cardinality, nested messages).
 * Returns the list of violations; throws ValidationError via assertValid.
 */
export function validateMessage(
  schema: MessageSchema,
  obj: MessageObject,
  path = schema.name,
): string[] {
  const violations: string[] = [];
  if (typeof obj !== "object" || obj === null || Array.isArray(obj)) {
    return [`${path}: expected an object`];
  }
  for (const spec of schema.fields) {
    const value = obj[spec.name];
    if (value === undefined || value === null) continue;
    const fieldPath = `${path}.${spec.name}`;
    const items = spec.repeated ? (value as unknown[]) : [value];
    if (spec.repeated && !Array.isArray(value)) {
      violations.push(`${fieldPath}: expected an array for repeated field`);
      continue;
    }
    items.forEach((item, i) => {
      const p = spec.repeated ? `${fieldPath}[${i}]` : fieldPath;
      checkValue(spec, item, p, violations);
    });
  }
  return violations;
}

function checkValue(spec: FieldSpec, value: unknown, path: string, out: string[]): void {
  if (spec.type === "message") {
    out.push(...validateMessage(spec.schema as MessageSchema, value as MessageObject, path));
    return;
  }
  const scalar = spec.type as ScalarType;
  switch (scalar) {
    case "bool":
      if (typeof value !== "boolean") out.push(`${path}: expected boolean, got ${typeof value}`);
      return;
    case "string":
      if (typeof value !== "string") out.push(`${path}: expected string, got ${typeof value}`);
      return;
    case "bytes":
      if (!(value instanceof Uint8Array)) out.push(`${path}: expected Uint8Array`);
      return;
    default: {
      const range = INT_RANGES[scalar] as [bigint, bigint];
      let v: bigint;
      if (typeof value === "bigint") v = value;
      else if (typeof value === "number" && Number.isInteger(value)) v = BigInt(value);
      else {
        out.push(`${path}: expected integer, got ${typeof value === "number" ? "non-integer number" : typeof value}`);
        return;
      }
      if (v < range[0] || v > range[1]) {
        out.push(`${path}: value ${v} out of range [${range[0]}, ${range[1]}] for ${scalar}`);
      }
    }
  }
}

export function assertValid(schema: MessageSchema, obj: MessageObject): void {
  const violations = validateMessage(schema, obj);
  if (violations.length > 0) throw new ValidationError(violations);
}
