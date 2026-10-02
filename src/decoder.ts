/**
 * Reflection-style schema-driven decoder: validates and materializes a
 * protobuf binary stream into plain structs according to a runtime schema.
 * Handles nested messages, packed/unpacked repeated fields, unknown-field
 * skipping, and hard bounds protection on every read.
 */

import {
  DecodeError,
  ErrorCode,
  WireReader,
  WireType,
  type WireTypeValue,
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

export interface FieldDescriptor {
  name: string;
  type: ScalarType | 'message';
  /** Nested schema, required when type === 'message'. */
  schema?: MessageSchema;
  repeated?: boolean;
  /** Packed encoding for repeated numeric fields (proto3 default). */
  packed?: boolean;
}

export interface MessageSchema {
  name: string;
  fields: Record<number, FieldDescriptor>;
}

export interface DecodeOptions {
  /** Maximum nesting depth for embedded messages. Default 64. */
  maxDepth?: number;
  /** Reject (instead of skip) unknown field numbers. Default false. */
  strict?: boolean;
}

const VARINT_TYPES: ReadonlySet<ScalarType> = new Set([
  'uint32',
  'uint64',
  'int32',
  'int64',
  'sint32',
  'sint64',
  'bool',
]);
const I64_TYPES: ReadonlySet<ScalarType> = new Set(['fixed64', 'sfixed64', 'double']);
const I32_TYPES: ReadonlySet<ScalarType> = new Set(['fixed32', 'sfixed32', 'float']);

/** Wire type a scalar uses when encoded standalone (unpacked). */
export function expectedWireType(fd: FieldDescriptor): WireTypeValue {
  if (fd.type === 'message' || fd.type === 'string' || fd.type === 'bytes') return WireType.LEN;
  if (VARINT_TYPES.has(fd.type)) return WireType.VARINT;
  if (I64_TYPES.has(fd.type)) return WireType.I64;
  if (I32_TYPES.has(fd.type)) return WireType.I32;
  throw new Error(`unmapped field type: ${fd.type}`);
}

function isPackable(fd: FieldDescriptor): boolean {
  return fd.type !== 'message' && fd.type !== 'string' && fd.type !== 'bytes';
}

function readScalar(r: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'uint32':
      return r.readUint32();
    case 'uint64':
      return r.readVarint64();
    case 'int32':
      return r.readInt32();
    case 'int64':
      return r.readInt64();
    case 'sint32':
      return r.readSInt32();
    case 'sint64':
      return r.readSInt64();
    case 'bool':
      return r.readBool();
    case 'fixed32':
      return r.readFixed32();
    case 'fixed64':
      return r.readFixed64();
    case 'sfixed32':
      return r.readSFixed32();
    case 'sfixed64':
      return r.readSFixed64();
    case 'float':
      return r.readFloat();
    case 'double':
      return r.readDouble();
    case 'string':
      return r.readString();
    case 'bytes':
      return r.readBytes();
  }
}

export interface DecodeStats {
  /** Fields matched against the schema. */
  knownFields: number;
  /** Fields skipped because they are absent from the schema. */
  unknownFields: number;
  /** Deepest message nesting actually reached. */
  maxDepthReached: number;
}

export interface DecodeResult {
  value: Record<string, unknown>;
  stats: DecodeStats;
}

export function decodeMessage(
  schema: MessageSchema,
  data: Uint8Array,
  options: DecodeOptions = {},
): DecodeResult {
  const stats: DecodeStats = { knownFields: 0, unknownFields: 0, maxDepthReached: 0 };
  const value = decodeInto(schema, new WireReader(data), options, 0, stats);
  return { value, stats };
}

function decodeInto(
  schema: MessageSchema,
  r: WireReader,
  options: DecodeOptions,
  depth: number,
  stats: DecodeStats,
): Record<string, unknown> {
  const maxDepth = options.maxDepth ?? 64;
  if (depth > maxDepth) {
    throw new DecodeError(
      ErrorCode.NESTING_TOO_DEEP,
      r.offset,
      `message nesting exceeds maxDepth=${maxDepth}`,
    );
  }
  if (depth > stats.maxDepthReached) stats.maxDepthReached = depth;

  const out: Record<string, unknown> = {};
  while (!r.done) {
    const { field, wire } = r.readTag();
    const fd = schema.fields[field];

    if (!fd) {
      stats.unknownFields++;
      if (options.strict) {
        throw new DecodeError(
          ErrorCode.INVALID_TAG,
          r.offset,
          `unknown field ${field} in message ${schema.name} (strict mode)`,
        );
      }
      r.skipField(wire);
      continue;
    }
    stats.knownFields++;

    if (fd.repeated && fd.packed && isPackable(fd) && wire === WireType.LEN) {
      const payload = r.readBytes();
      const sub = new WireReader(payload);
      const arr: unknown[] = [];
      while (!sub.done) {
        arr.push(readScalar(sub, fd.type as ScalarType));
      }
      const existing = out[fd.name] as unknown[] | undefined;
      out[fd.name] = existing ? existing.concat(arr) : arr;
      continue;
    }

    const want = expectedWireType(fd);
    if (wire !== want) {
      throw new DecodeError(
        ErrorCode.INVALID_WIRE_TYPE,
        r.offset,
        `field ${fd.name} expects wire type ${want}, got ${wire}`,
      );
    }

    let v: unknown;
    if (fd.type === 'message') {
      if (!fd.schema) {
        throw new DecodeError(ErrorCode.INVALID_TAG, r.offset, `field ${fd.name} lacks nested schema`);
      }
      const payload = r.readBytes();
      v = decodeInto(fd.schema, new WireReader(payload), options, depth + 1, stats);
    } else {
      v = readScalar(r, fd.type);
    }

    if (fd.repeated) {
      const arr = (out[fd.name] as unknown[] | undefined) ?? [];
      arr.push(v);
      out[fd.name] = arr;
    } else {
      out[fd.name] = v;
    }
  }
  return out;
}
