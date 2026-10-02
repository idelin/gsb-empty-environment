import { MalformedError, SchemaValidationError } from "./errors.ts";
import {
  type DecodedMessage,
  type FieldDescriptor,
  type MessageSchema,
  isPackable,
  wireTypeOf,
} from "./schema.ts";
import { toSigned64, toUnsigned64, zigzagDecode, zigzagEncode } from "./varint.ts";
import { Reader, WireType, Writer } from "./wire.ts";

const INT32_MIN = -0x8000_0000;
const INT32_MAX = 0x7fff_ffff;
const UINT32_MAX = 0xffff_ffff;
const INT64_MIN = -(1n << 63n);
const INT64_MAX = (1n << 63n) - 1n;
const UINT64_MAX = (1n << 64n) - 1n;

/* ------------------------------ validation ------------------------------ */

function checkScalar(field: FieldDescriptor, value: unknown, path: string, issues: string[]): void {
  const where = `${path}.${field.name} (#${field.no})`;
  const fail = (msg: string) => issues.push(`${where}: ${msg}`);
  switch (field.type) {
    case "int32":
    case "sint32":
    case "sfixed32":
    case "enum":
      if (typeof value !== "number" || !Number.isInteger(value) || value < INT32_MIN || value > INT32_MAX)
        fail(`expected int32, got ${String(value)}`);
      return;
    case "uint32":
    case "fixed32":
      if (typeof value !== "number" || !Number.isInteger(value) || value < 0 || value > UINT32_MAX)
        fail(`expected uint32, got ${String(value)}`);
      return;
    case "int64":
    case "sint64":
    case "sfixed64": {
      if (typeof value !== "bigint" && typeof value !== "number") {
        fail(`expected int64, got ${typeof value}`);
        return;
      }
      const v = BigInt(value);
      if (v < INT64_MIN || v > INT64_MAX) fail(`int64 out of range: ${v}`);
      return;
    }
    case "uint64":
    case "fixed64": {
      if (typeof value !== "bigint" && typeof value !== "number") {
        fail(`expected uint64, got ${typeof value}`);
        return;
      }
      const v = BigInt(value);
      if (v < 0n || v > UINT64_MAX) fail(`uint64 out of range: ${v}`);
      return;
    }
    case "bool":
      if (typeof value !== "boolean") fail(`expected bool, got ${typeof value}`);
      return;
    case "float":
    case "double":
      if (typeof value !== "number") fail(`expected number, got ${typeof value}`);
      return;
    case "string":
      if (typeof value !== "string") fail(`expected string, got ${typeof value}`);
      return;
    case "bytes":
      if (!(value instanceof Uint8Array)) fail("expected Uint8Array");
      return;
    case "message":
      if (typeof value !== "object" || value === null) fail("expected message object");
      else validateInto(field.message!, value as Record<string, unknown>, where, issues);
      return;
  }
}

function validateInto(
  schema: MessageSchema,
  obj: Record<string, unknown>,
  path: string,
  issues: string[],
): void {
  for (const field of schema.fields) {
    const value = obj[field.name];
    if (value === undefined || value === null) {
      if (field.required) issues.push(`${path}.${field.name} (#${field.no}): missing required field`);
      continue;
    }
    if (field.repeated) {
      if (!Array.isArray(value)) {
        issues.push(`${path}.${field.name} (#${field.no}): expected array`);
        continue;
      }
      if (field.packed && !isPackable(field.type)) {
        issues.push(`${path}.${field.name} (#${field.no}): type ${field.type} is not packable`);
      }
      value.forEach((item, i) => checkScalar(field, item, `${path}[${i}]`, issues));
    } else {
      checkScalar(field, value, path, issues);
    }
  }
}

/** Validate a plain object against the schema; throws on any violation. */
export function validateMessage(schema: MessageSchema, obj: Record<string, unknown>): void {
  const issues: string[] = [];
  validateInto(schema, obj, schema.name, issues);
  if (issues.length > 0) throw new SchemaValidationError(issues);
}

/* ------------------------------- encoding ------------------------------- */

function writeVarintScalar(w: Writer, field: FieldDescriptor, value: unknown): void {
  switch (field.type) {
    case "int32":
    case "enum":
    case "int64":
      // Negative values are sign-extended to 64 bits (10-byte varint), per spec.
      w.varint(toUnsigned64(BigInt.asIntN(64, BigInt(value as bigint | number))));
      return;
    case "uint32":
      w.varint(value as number);
      return;
    case "uint64":
      w.varint(value as bigint | number);
      return;
    case "sint32":
    case "sint64":
      w.varint(zigzagEncode(BigInt(value as bigint | number)));
      return;
    case "bool":
      w.varint(value ? 1 : 0);
      return;
    default:
      throw new MalformedError(`type ${field.type} is not varint-encodable`);
  }
}

function writeScalar(w: Writer, field: FieldDescriptor, value: unknown): void {
  if (wireTypeOf(field.type) === WireType.Varint) {
    writeVarintScalar(w, field, value);
    return;
  }
  switch (field.type) {
    case "fixed32":
      w.fixed32(value as number);
      return;
    case "fixed64":
      w.fixed64(value as bigint | number);
      return;
    case "sfixed32":
      w.sfixed32(value as number);
      return;
    case "sfixed64":
      w.sfixed64(value as bigint | number);
      return;
    case "float":
      w.float(value as number);
      return;
    case "double":
      w.double(value as number);
      return;
    case "string":
      w.string(value as string);
      return;
    case "bytes":
      w.bytes(value as Uint8Array);
      return;
    case "message": {
      const nested = encodeMessage(field.message!, value as Record<string, unknown>);
      w.bytes(nested);
      return;
    }
  }
}

/** Encode a validated plain object into wire-format bytes. */
export function encodeMessage(
  schema: MessageSchema,
  obj: Record<string, unknown>,
  { validate = true }: { validate?: boolean } = {},
): Uint8Array {
  if (validate) validateMessage(schema, obj);
  const w = new Writer();
  for (const field of schema.fields) {
    const value = obj[field.name];
    if (value === undefined || value === null) continue;
    if (field.repeated) {
      const items = value as unknown[];
      if (items.length === 0) continue;
      if (field.packed) {
        const inner = new Writer();
        for (const item of items) writeScalar(inner, field, item);
        w.tag(field.no, WireType.LengthDelimited);
        w.bytes(inner.finish());
      } else {
        for (const item of items) {
          w.tag(field.no, wireTypeOf(field.type));
          writeScalar(w, field, item);
        }
      }
    } else {
      w.tag(field.no, wireTypeOf(field.type));
      writeScalar(w, field, value);
    }
  }
  return w.finish();
}

/* ------------------------------- decoding ------------------------------- */

function readScalar(r: Reader, field: FieldDescriptor, path: string): unknown {
  switch (field.type) {
    case "int32":
    case "enum":
      return Number(BigInt.asIntN(32, r.varint()));
    case "int64":
      return toSigned64(r.varint());
    case "uint32":
      return Number(r.varint() & 0xffff_ffffn);
    case "uint64":
      return r.varint();
    case "sint32":
      return Number(BigInt.asIntN(32, zigzagDecode(r.varint())));
    case "sint64":
      return zigzagDecode(r.varint());
    case "bool":
      return r.varint() !== 0n;
    case "fixed32":
      return r.fixed32();
    case "fixed64":
      return r.fixed64();
    case "sfixed32":
      return r.sfixed32();
    case "sfixed64":
      return r.sfixed64();
    case "float":
      return r.float();
    case "double":
      return r.double();
    case "string":
      return r.string();
    case "bytes":
      return r.bytes();
    case "message": {
      const child = r.subMessage(path);
      return decodeFromReader(field.message!, child, path);
    }
  }
}

function readPacked(r: Reader, field: FieldDescriptor, path: string): unknown[] {
  const block = r.bytes();
  const inner = new Reader(block);
  inner.fieldPath = `${path}[packed]`;
  const out: unknown[] = [];
  while (!inner.done) out.push(readScalar(inner, field, inner.fieldPath));
  return out;
}

function recordUnknown(out: DecodedMessage, fieldNo: number, value: unknown): void {
  const bag = (out.$unknown ??= {});
  (bag[fieldNo] ??= []).push(value);
}

function decodeFromReader(schema: MessageSchema, r: Reader, path: string): DecodedMessage {
  const out: DecodedMessage = {};
  const byNo = new Map(schema.fields.map((f) => [f.no, f]));
  while (!r.done) {
    const { fieldNo, wireType } = r.tag();
    const field = byNo.get(fieldNo);
    if (!field) {
      // Unknown field: skip safely, record raw location for round-tripping.
      const before = r.offset;
      r.skip(wireType);
      recordUnknown(out, fieldNo, { wireType, bytes: before });
      continue;
    }
    const fieldPath = path ? `${path}.${field.name}` : field.name;
    r.fieldPath = fieldPath;
    const expected = wireTypeOf(field.type);
    if (wireType === expected) {
      const value = readScalar(r, field, fieldPath);
      if (field.repeated) (out[field.name] as unknown[] ??= []).push(value);
      else out[field.name] = value;
    } else if (field.repeated && isPackable(field.type) && wireType === WireType.LengthDelimited) {
      // Packed encoding on the wire: accept whether or not the schema packs.
      const items = readPacked(r, field, fieldPath);
      (out[field.name] as unknown[] ??= []).push(...items);
    } else {
      throw new MalformedError(
        `field ${fieldPath} (#${field.no}): wire type ${wireType}, expected ${expected}`,
        r.offset,
      );
    }
  }
  return out;
}

export interface DecodeOptions {
  /** Verify required fields are present after decoding. Default true. */
  checkRequired?: boolean;
}

/**
 * Decode wire-format bytes into a plain object per the schema.
 * Truncated input raises {@link TruncationError} carrying the field path;
 * malformed tags raise {@link MalformedError}. Never reads out of bounds.
 */
export function decodeMessage(
  schema: MessageSchema,
  bytes: Uint8Array,
  options: DecodeOptions = {},
): DecodedMessage {
  const { checkRequired = true } = options;
  const reader = new Reader(bytes);
  const out = decodeFromReader(schema, reader, schema.name);
  if (checkRequired) {
    const missing = schema.fields
      .filter((f) => f.required && out[f.name] === undefined)
      .map((f) => `${schema.name}.${f.name} (#${f.no})`);
    if (missing.length > 0) {
      throw new SchemaValidationError([`missing required field(s): ${missing.join(", ")}`]);
    }
  }
  return out;
}
