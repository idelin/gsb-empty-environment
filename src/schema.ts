/**
 * Reflection-style schema validation engine.
 *
 * Decodes a protobuf binary stream into a plain object graph driven by a
 * declarative schema. Unknown fields are skipped safely; wire-type
 * mismatches raise SchemaError with a dotted field path; any truncation
 * surfaces as DecodeError from the underlying WireReader.
 */

import { DecodeError, WireReader, WireType } from './wire.ts';

export type ScalarType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64' | 'bool'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'float' | 'double'
  | 'string' | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldSpec {
  /** Protobuf field number (>= 1). */
  readonly no: number;
  readonly name: string;
  readonly type: FieldType;
  readonly repeated?: boolean;
  /** Packed encoding for repeated numeric fields (proto3 default: true). */
  readonly packed?: boolean;
  /** Nested schema, required when type === 'message'. */
  readonly schema?: Schema;
}

export type Schema = readonly FieldSpec[];

export class SchemaError extends Error {
  readonly path: string;
  constructor(message: string, path: string) {
    super(`${message} (at ${path || '<root>'})`);
    this.name = 'SchemaError';
    this.path = path;
  }
}

const NUMERIC_TYPES: ReadonlySet<ScalarType> = new Set([
  'int32', 'int64', 'uint32', 'uint64', 'sint32', 'sint64', 'bool',
  'fixed32', 'fixed64', 'sfixed32', 'sfixed64', 'float', 'double',
]);

function expectedWireType(type: FieldType): WireType {
  switch (type) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
    case 'sint32': case 'sint64': case 'bool':
      return WireType.Varint;
    case 'fixed64': case 'sfixed64': case 'double':
      return WireType.Fixed64;
    case 'fixed32': case 'sfixed32': case 'float':
      return WireType.Fixed32;
    case 'string': case 'bytes': case 'message':
      return WireType.LengthDelimited;
  }
}

function readScalar(reader: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'int32': return reader.readInt32();
    case 'int64': return reader.readInt64();
    case 'uint32': return reader.readUint32();
    case 'uint64': return reader.readUint64();
    case 'sint32': return reader.readSint32();
    case 'sint64': return reader.readSint64();
    case 'bool': return reader.readBool();
    case 'fixed32': return reader.readFixed32();
    case 'fixed64': return reader.readFixed64();
    case 'sfixed32': return reader.readSfixed32();
    case 'sfixed64': return reader.readSfixed64();
    case 'float': return reader.readFloat();
    case 'double': return reader.readDouble();
    case 'string': return new TextDecoder().decode(reader.readLengthDelimited());
    case 'bytes': return reader.readLengthDelimited();
  }
}

function joinPath(parent: string, name: string): string {
  return parent === '' ? name : `${parent}.${name}`;
}

/**
 * Decodes one message from `input` according to `schema`.
 *
 * - Unknown field numbers are skipped with bounds-checked skips.
 * - Repeated numeric fields accept both packed (length-delimited) and
 *   unpacked encodings, and may mix the two.
 * - Repeated fields always materialize as arrays (empty when absent).
 */
export function decodeMessage(
  schema: Schema,
  input: WireReader | Uint8Array,
  path = '',
): Record<string, unknown> {
  const reader = input instanceof WireReader ? input : new WireReader(input);
  const byNumber = new Map<number, FieldSpec>();
  for (const spec of schema) {
    if (!Number.isInteger(spec.no) || spec.no < 1) {
      throw new SchemaError(`invalid field number ${spec.no}`, joinPath(path, spec.name));
    }
    byNumber.set(spec.no, spec);
  }

  const out: Record<string, unknown> = {};
  for (const spec of schema) {
    if (spec.repeated) out[spec.name] = [];
  }

  while (!reader.done) {
    const tag = reader.readTag();
    if (tag === null) break;
    const spec = byNumber.get(tag.field);
    if (spec === undefined) {
      reader.skipField(tag.wireType);
      continue;
    }
    const fieldPath = joinPath(path, spec.name);

    if (spec.repeated && spec.type !== 'message' &&
        NUMERIC_TYPES.has(spec.type as ScalarType) &&
        tag.wireType === WireType.LengthDelimited) {
      // Packed repeated: a length-delimited run of back-to-back values.
      const sub = reader.readSubReader();
      const arr = out[spec.name] as unknown[];
      while (!sub.done) {
        arr.push(readScalar(sub, spec.type as ScalarType));
      }
      continue;
    }

    const expected = expectedWireType(spec.type);
    if (tag.wireType !== expected) {
      throw new SchemaError(
        `wire type mismatch for "${spec.name}": expected ${expected}, got ${tag.wireType}`,
        fieldPath,
      );
    }

    let value: unknown;
    if (spec.type === 'message') {
      if (spec.schema === undefined) {
        throw new SchemaError(`missing nested schema for "${spec.name}"`, fieldPath);
      }
      value = decodeMessage(spec.schema, reader.readSubReader(), fieldPath);
    } else {
      value = readScalar(reader, spec.type as ScalarType);
    }

    if (spec.repeated) {
      (out[spec.name] as unknown[]).push(value);
    } else {
      out[spec.name] = value;
    }
  }

  return out;
}

export { DecodeError };
