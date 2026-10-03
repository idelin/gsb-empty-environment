import { ValidationError } from './errors.ts';
import { WireReader } from './reader.ts';
import { WireType } from './wire-type.ts';
import { WireWriter } from './writer.ts';

export type ScalarType =
  | 'uint32' | 'int32' | 'uint64' | 'int64'
  | 'sint32' | 'sint64'
  | 'bool'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'float' | 'double'
  | 'string' | 'bytes';

export interface FieldSpec {
  /** Protobuf field number, 1..2^29-1. */
  readonly no: number;
  readonly name: string;
  readonly type: ScalarType | 'message';
  readonly repeated?: boolean;
  /** Encode as a packed length-delimited run (numeric scalars only).
   *  Decoding always accepts both packed and unpacked encodings. */
  readonly packed?: boolean;
  /** Nested message schema, required when type === 'message'. */
  readonly schema?: Schema;
  /** Value used when the field is absent on the wire (backward compat). */
  readonly default?: unknown;
}

export type Schema = readonly FieldSpec[];
export type MessageObject = Record<string, unknown>;

const WIRE_TYPE_OF: Record<ScalarType, number> = {
  uint32: WireType.Varint, int32: WireType.Varint,
  uint64: WireType.Varint, int64: WireType.Varint,
  sint32: WireType.Varint, sint64: WireType.Varint,
  bool: WireType.Varint,
  fixed32: WireType.Fixed32, sfixed32: WireType.Fixed32, float: WireType.Fixed32,
  fixed64: WireType.Fixed64, sfixed64: WireType.Fixed64, double: WireType.Fixed64,
  string: WireType.LengthDelimited, bytes: WireType.LengthDelimited,
};

const PACKABLE = new Set<ScalarType>([
  'uint32', 'int32', 'uint64', 'int64', 'sint32', 'sint64', 'bool',
  'fixed32', 'fixed64', 'sfixed32', 'sfixed64', 'float', 'double',
]);

function fieldMap(schema: Schema): Map<number, FieldSpec> {
  const m = new Map<number, FieldSpec>();
  for (const f of schema) m.set(f.no, f);
  return m;
}

// ---------------------------------------------------------------------------
// Reflection-mode validation engine
// ---------------------------------------------------------------------------

const INT_RANGES: Partial<Record<ScalarType, [bigint, bigint]>> = {
  uint32: [0n, 0xffff_ffffn],
  int32: [-0x8000_0000n, 0x7fff_ffffn],
  sint32: [-0x8000_0000n, 0x7fff_ffffn],
  fixed32: [0n, 0xffff_ffffn],
  sfixed32: [-0x8000_0000n, 0x7fff_ffffn],
  uint64: [0n, 0xffff_ffff_ffff_ffffn],
  int64: [-0x8000_0000_0000_0000n, 0x7fff_ffff_ffff_ffffn],
  sint64: [-0x8000_0000_0000_0000n, 0x7fff_ffff_ffff_ffffn],
  fixed64: [0n, 0xffff_ffff_ffff_ffffn],
  sfixed64: [-0x8000_0000_0000_0000n, 0x7fff_ffff_ffff_ffffn],
};

function validateScalar(type: ScalarType, value: unknown, path: string): void {
  const fail = (why: string): never => {
    throw new ValidationError(why, path);
  };
  switch (type) {
    case 'string':
      if (typeof value !== 'string') fail(`expected string, got ${typeof value}`);
      return;
    case 'bytes':
      if (!(value instanceof Uint8Array)) fail('expected Uint8Array');
      return;
    case 'bool':
      if (typeof value !== 'boolean') fail(`expected boolean, got ${typeof value}`);
      return;
    case 'float':
    case 'double':
      if (typeof value !== 'number') fail(`expected number, got ${typeof value}`);
      return;
    default: {
      const range = INT_RANGES[type]!;
      const is64 = range[1] > 0xffff_ffffn || range[0] < -0x8000_0000n;
      if (is64 ? typeof value !== 'bigint' : typeof value !== 'number') {
        fail(`expected ${is64 ? 'bigint' : 'number'}, got ${typeof value}`);
      }
      if (!Number.isInteger(typeof value === 'number' ? value : 0) && typeof value === 'number') {
        fail('expected an integer');
      }
      const v = typeof value === 'bigint' ? value : BigInt(value as number);
      if (v < range[0] || v > range[1]) {
        fail(`value ${v} out of range [${range[0]}, ${range[1]}] for ${type}`);
      }
    }
  }
}

/** Recursively validate an object against a schema before encoding. */
export function validate(schema: Schema, obj: MessageObject, path = '$'): void {
  for (const f of schema) {
    const p = `${path}.${f.name}`;
    let value = obj[f.name];
    if (value === undefined || value === null) {
      if (f.default !== undefined) value = f.default;
      else continue; // absent optional field
    }
    if (f.repeated) {
      if (!Array.isArray(value)) throw new ValidationError('expected an array', p);
      if (f.packed && f.type !== 'message' && !PACKABLE.has(f.type as ScalarType)) {
        throw new ValidationError(`type ${f.type} cannot be packed`, p);
      }
      value.forEach((item, i) => validateOne(f, item, `${p}[${i}]`));
    } else {
      validateOne(f, value, p);
    }
  }
}

function validateOne(f: FieldSpec, value: unknown, path: string): void {
  if (f.type === 'message') {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      throw new ValidationError('expected a message object', path);
    }
    if (!f.schema) throw new ValidationError('field schema missing for message type', path);
    validate(f.schema, value as MessageObject, path);
  } else {
    validateScalar(f.type as ScalarType, value, path);
  }
}

// ---------------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------------

function writeScalar(w: WireWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case 'uint32': w.uint32(value as number); return;
    case 'int32': w.int32(value as number); return;
    case 'uint64': w.uint64(value as bigint); return;
    case 'int64': w.int64(value as bigint); return;
    case 'sint32': w.zigzag32(value as number); return;
    case 'sint64': w.zigzag64(value as bigint); return;
    case 'bool': w.varint(value ? 1 : 0); return;
    case 'fixed32': w.fixed32(value as number); return;
    case 'sfixed32': w.sfixed32(value as number); return;
    case 'float': w.float(value as number); return;
    case 'fixed64': w.fixed64(value as bigint); return;
    case 'sfixed64': w.sfixed64(value as bigint); return;
    case 'double': w.double(value as number); return;
    case 'string': w.string(value as string); return;
    case 'bytes': w.bytes(value as Uint8Array); return;
  }
}

function encodeField(w: WireWriter, f: FieldSpec, value: unknown): void {
  if (f.repeated) {
    const items = value as unknown[];
    if (items.length === 0) return;
    if (f.packed && f.type !== 'message') {
      const inner = new WireWriter();
      for (const item of items) writeScalar(inner, f.type as ScalarType, item);
      w.tag(f.no, WireType.LengthDelimited).bytes(inner.finish());
      return;
    }
    for (const item of items) encodeOne(w, f, item);
    return;
  }
  encodeOne(w, f, value);
}

function encodeOne(w: WireWriter, f: FieldSpec, value: unknown): void {
  if (f.type === 'message') {
    const body = encode(f.schema!, value as MessageObject);
    w.tag(f.no, WireType.LengthDelimited).bytes(body);
    return;
  }
  const type = f.type as ScalarType;
  w.tag(f.no, WIRE_TYPE_OF[type]);
  writeScalar(w, type, value);
}

/** Validate (reflection) then serialize an object to wire-format bytes. */
export function encode(schema: Schema, obj: MessageObject, opts: { validate?: boolean } = {}): Uint8Array {
  if (opts.validate !== false) validate(schema, obj);
  const w = new WireWriter();
  for (const f of schema) {
    let value = obj[f.name];
    if (value === undefined || value === null) {
      if (f.default === undefined) continue;
      value = f.default;
    }
    encodeField(w, f, value);
  }
  return w.finish();
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

function readScalar(r: WireReader, type: ScalarType): unknown {
  switch (type) {
    case 'uint32': return r.uint32();
    case 'int32': return r.int32();
    case 'uint64': return r.uint64();
    case 'int64': return r.int64();
    case 'sint32': return r.zigzag32();
    case 'sint64': return r.zigzag64();
    case 'bool': return r.bool();
    case 'fixed32': return r.fixed32();
    case 'sfixed32': return r.sfixed32();
    case 'float': return r.float();
    case 'fixed64': return r.fixed64();
    case 'sfixed64': return r.sfixed64();
    case 'double': return r.double();
    case 'string': return r.string();
    case 'bytes': return r.bytes();
  }
}

function defaultFor(f: FieldSpec): unknown {
  if (f.default !== undefined) return f.default;
  if (f.repeated) return [];
  switch (f.type) {
    case 'message': return undefined;
    case 'string': return '';
    case 'bytes': return new Uint8Array(0);
    case 'bool': return false;
    case 'float': case 'double': return 0;
    case 'uint64': case 'int64': case 'sint64':
    case 'fixed64': case 'sfixed64': return 0n;
    default: return 0;
  }
}

/**
 * Parse wire-format bytes into an object per the schema.
 *
 * - Unknown field numbers are skipped (forward compatibility).
 * - Missing fields fall back to schema defaults (backward compatibility).
 * - Packable scalars accept both packed (LEN) and unpacked encodings.
 * - Nested messages are parsed over zero-copy subarray views of `buf`.
 * - Every read is bounds-checked; truncation raises TruncatedError.
 */
export function decode(schema: Schema, buf: Uint8Array): MessageObject {
  const byNo = fieldMap(schema);
  const out: MessageObject = {};
  const r = new WireReader(buf);
  while (!r.done) {
    const [no, wire] = r.tag();
    const f = byNo.get(no);
    if (!f) {
      r.skip(wire);
      continue;
    }
    if (f.type === 'message') {
      if (wire !== WireType.LengthDelimited) r.skip(wire);
      else {
        const sub = decode(f.schema!, r.bytes());
        if (f.repeated) ((out[f.name] ??= []) as unknown[]).push(sub);
        else out[f.name] = sub;
      }
      continue;
    }
    const type = f.type as ScalarType;
    if (f.repeated && PACKABLE.has(type) && wire === WireType.LengthDelimited) {
      const packed = new WireReader(r.bytes());
      const arr = (out[f.name] ??= []) as unknown[];
      while (!packed.done) arr.push(readScalar(packed, type));
      continue;
    }
    if (wire !== WIRE_TYPE_OF[type]) {
      r.skip(wire); // mismatched but skippable encoding: tolerate
      continue;
    }
    const value = readScalar(r, type);
    if (f.repeated) ((out[f.name] ??= []) as unknown[]).push(value);
    else out[f.name] = value;
  }
  for (const f of schema) {
    if (out[f.name] === undefined) {
      const d = defaultFor(f);
      if (d !== undefined) out[f.name] = d;
    }
  }
  return out;
}
