import { ProtobufValidationError, ProtobufWireError } from './errors.ts';
import { Reader } from './reader.ts';
import { Writer } from './writer.ts';
import { WireType } from './wire.ts';

/** Scalar field types plus nested messages described by a MessageDescriptor. */
export type ScalarType =
  | 'int32'
  | 'int64'
  | 'uint32'
  | 'uint64'
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

export type FieldType = ScalarType | MessageDescriptor;

export interface FieldDescriptor {
  name: string;
  number: number;
  type: FieldType;
  repeated?: boolean;
  /** Encode repeated scalar numerics as a single length-delimited block. */
  packed?: boolean;
}

export interface MessageDescriptor {
  name: string;
  fields: FieldDescriptor[];
}

const WIRE_TYPE_OF: Record<ScalarType, WireType> = {
  int32: WireType.Varint,
  int64: WireType.Varint,
  uint32: WireType.Varint,
  uint64: WireType.Varint,
  sint32: WireType.Varint,
  sint64: WireType.Varint,
  bool: WireType.Varint,
  fixed32: WireType.Fixed32,
  fixed64: WireType.Fixed64,
  sfixed32: WireType.Fixed32,
  sfixed64: WireType.Fixed64,
  float: WireType.Fixed32,
  double: WireType.Fixed64,
  string: WireType.LengthDelimited,
  bytes: WireType.LengthDelimited,
};

function isMessage(type: FieldType): type is MessageDescriptor {
  return typeof type === 'object';
}

function wireTypeOf(type: FieldType): WireType {
  return isMessage(type) ? WireType.LengthDelimited : WIRE_TYPE_OF[type];
}

/** Numeric types that may appear inside a packed repeated block. */
function isPackable(type: FieldType): boolean {
  return !isMessage(type) && type !== 'string' && type !== 'bytes';
}

function fail(field: string, message: string): never {
  throw new ProtobufValidationError(field, message);
}

function checkIntRange(field: string, v: bigint, min: bigint, max: bigint): void {
  if (v < min || v > max) {
    fail(field, `value ${v} out of range [${min}, ${max}]`);
  }
}

const I32_MIN = -(1n << 31n);
const I32_MAX = (1n << 31n) - 1n;
const U32_MAX = (1n << 32n) - 1n;
const I64_MIN = -(1n << 63n);
const I64_MAX = (1n << 63n) - 1n;
const U64_MAX = (1n << 64n) - 1n;

/** Validate a single JS value against its declared scalar type. */
function validateScalar(field: string, type: ScalarType, value: unknown): void {
  switch (type) {
    case 'int32':
    case 'sint32':
    case 'sfixed32':
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        fail(field, `expected integer, got ${typeof value}`);
      }
      checkIntRange(field, BigInt(value), I32_MIN, I32_MAX);
      return;
    case 'uint32':
    case 'fixed32':
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        fail(field, `expected integer, got ${typeof value}`);
      }
      checkIntRange(field, BigInt(value), 0n, U32_MAX);
      return;
    case 'int64':
    case 'sint64':
    case 'sfixed64': {
      if (typeof value !== 'bigint' && typeof value !== 'number') {
        fail(field, `expected bigint|number, got ${typeof value}`);
      }
      checkIntRange(field, BigInt(value), I64_MIN, I64_MAX);
      return;
    }
    case 'uint64':
    case 'fixed64': {
      if (typeof value !== 'bigint' && typeof value !== 'number') {
        fail(field, `expected bigint|number, got ${typeof value}`);
      }
      checkIntRange(field, BigInt(value), 0n, U64_MAX);
      return;
    }
    case 'bool':
      if (typeof value !== 'boolean') fail(field, `expected boolean, got ${typeof value}`);
      return;
    case 'float':
    case 'double':
      if (typeof value !== 'number') fail(field, `expected number, got ${typeof value}`);
      return;
    case 'string':
      if (typeof value !== 'string') fail(field, `expected string, got ${typeof value}`);
      return;
    case 'bytes':
      if (!(value instanceof Uint8Array)) fail(field, 'expected Uint8Array');
      return;
  }
}

function writeScalar(w: Writer, type: ScalarType, value: never): void {
  switch (type) {
    case 'int32': w.int32(value); return;
    case 'int64': w.int64(value); return;
    case 'uint32': w.uint32(value); return;
    case 'uint64': w.uint64(value); return;
    case 'sint32': w.sint32(value); return;
    case 'sint64': w.sint64(value); return;
    case 'bool': w.bool(value); return;
    case 'fixed32': w.fixed32(value); return;
    case 'fixed64': w.fixed64(value); return;
    case 'sfixed32': w.sfixed32(value); return;
    case 'sfixed64': w.sfixed64(value); return;
    case 'float': w.float(value); return;
    case 'double': w.double(value); return;
    case 'string': w.string(value); return;
    case 'bytes': w.bytes(value); return;
  }
}

function readScalar(r: Reader, type: ScalarType): unknown {
  switch (type) {
    case 'int32': return r.int32();
    case 'int64': return r.int64();
    case 'uint32': return r.uint32();
    case 'uint64': return r.uint64();
    case 'sint32': return r.sint32();
    case 'sint64': return r.sint64();
    case 'bool': return r.bool();
    case 'fixed32': return r.fixed32();
    case 'fixed64': return r.fixed64();
    case 'sfixed32': return r.sfixed32();
    case 'sfixed64': return r.sfixed64();
    case 'float': return r.float();
    case 'double': return r.double();
    case 'string': return r.string();
    case 'bytes': return r.bytes();
  }
}

/**
 * Reflection-style validation engine: encodes a plain JS object into protobuf
 * wire format according to a MessageDescriptor, validating every field's
 * presence, type and range along the way.
 */
export function encodeMessage(
  desc: MessageDescriptor,
  obj: Record<string, unknown>
): Uint8Array {
  if (obj === null || typeof obj !== 'object') {
    fail(desc.name, 'expected an object to encode');
  }
  const w = new Writer();
  for (const field of desc.fields) {
    const value = obj[field.name];
    if (value === undefined || value === null) continue;

    if (field.repeated) {
      if (!Array.isArray(value)) {
        fail(field.name, 'expected an array for repeated field');
      }
      if (field.packed) {
        if (!isPackable(field.type)) {
          fail(field.name, 'only scalar numeric fields can be packed');
        }
        if (value.length === 0) continue;
        const inner = new Writer();
        for (const item of value) {
          validateScalar(field.name, field.type as ScalarType, item);
          writeScalar(inner, field.type as ScalarType, item as never);
        }
        w.tag(field.number, WireType.LengthDelimited);
        w.bytes(inner.finish());
      } else {
        for (const item of value) {
          writeField(w, field, item);
        }
      }
    } else {
      writeField(w, field, value);
    }
  }
  return w.finish();
}

function writeField(w: Writer, field: FieldDescriptor, value: unknown): void {
  if (isMessage(field.type)) {
    const nested = encodeMessage(field.type, value as Record<string, unknown>);
    w.tag(field.number, WireType.LengthDelimited);
    w.bytes(nested);
    return;
  }
  validateScalar(field.name, field.type, value);
  w.tag(field.number, WIRE_TYPE_OF[field.type]);
  writeScalar(w, field.type, value as never);
}

/**
 * Decode a protobuf wire-format buffer into a plain JS object according to a
 * MessageDescriptor. Unknown fields are skipped; wire-type mismatches and
 * truncated data raise descriptive errors without ever reading out of bounds.
 */
export function decodeMessage(
  desc: MessageDescriptor,
  buf: Uint8Array
): Record<string, unknown> {
  const byNumber = new Map<number, FieldDescriptor>();
  for (const f of desc.fields) byNumber.set(f.number, f);

  const out: Record<string, unknown> = {};
  for (const f of desc.fields) {
    if (f.repeated) out[f.name] = [];
  }

  const r = new Reader(buf);
  for (;;) {
    const tag = r.tag();
    if (tag === null) break;
    const field = byNumber.get(tag.fieldNumber);
    if (!field) {
      r.skip(tag.wireType);
      continue;
    }

    if (field.repeated && isPackable(field.type)) {
      if (tag.wireType === WireType.LengthDelimited) {
        // Packed block: decode until the delimited sub-buffer is exhausted.
        const block = new Reader(r.bytes());
        const arr = out[field.name] as unknown[];
        while (!block.eof) {
          arr.push(readScalar(block, field.type as ScalarType));
        }
        continue;
      }
      // Non-packed encoding of a packable field is also accepted.
    }

    const expected = wireTypeOf(field.type);
    if (tag.wireType !== expected) {
      throw new ProtobufWireError(
        `field "${field.name}" (#${field.number}): expected wire type ${expected}, got ${tag.wireType}`
      );
    }

    let value: unknown;
    if (isMessage(field.type)) {
      value = decodeMessage(field.type, r.bytes());
    } else {
      value = readScalar(r, field.type);
    }

    if (field.repeated) {
      (out[field.name] as unknown[]).push(value);
    } else {
      out[field.name] = value;
    }
  }
  return out;
}
