/**
 * Reflection-style schema validation & decode engine.
 *
 * A MessageSchema describes fields declaratively; decodeMessage walks the
 * wire stream, validates each tag's wire type against the schema, decodes
 * values (recursing into nested length-delimited messages and packed
 * repeated arrays), and skips unknown fields safely.
 *
 * Pass a `target` object to reuse it (and its arrays / sub-objects) across
 * decodes — the steady-state hot path then allocates nothing. Field lookup
 * tables are compiled once per schema and cached in a WeakMap.
 */
import {
  TruncatedFieldError,
  WireTypeMismatchError,
} from "./errors.ts";
import {
  WIRE_FIXED32,
  WIRE_FIXED64,
  WIRE_LENGTH_DELIMITED,
  WIRE_VARINT,
  WireReader,
} from "./reader.ts";

export type FieldKind =
  | "varint" // uint32/uint64-ish (number, safe range)
  | "varint64" // full 64-bit as bigint
  | "zigzag32"
  | "zigzag64"
  | "fixed32"
  | "fixed64"
  | "bytes"
  | "string"
  | "message"
  | "packed-varint"
  | "packed-zigzag32"
  | "packed-zigzag64"
  | "packed-fixed32"
  | "packed-fixed64";

export interface FieldSpec {
  field: number;
  name: string;
  kind: FieldKind;
  repeated?: boolean; // non-packed repeated (one tag per element)
  schema?: MessageSchema; // required when kind === "message"
}

export interface MessageSchema {
  name: string;
  fields: FieldSpec[];
}

const EXPECTED_WIRE: Record<FieldKind, number> = {
  varint: WIRE_VARINT,
  varint64: WIRE_VARINT,
  zigzag32: WIRE_VARINT,
  zigzag64: WIRE_VARINT,
  fixed32: WIRE_FIXED32,
  fixed64: WIRE_FIXED64,
  bytes: WIRE_LENGTH_DELIMITED,
  string: WIRE_LENGTH_DELIMITED,
  message: WIRE_LENGTH_DELIMITED,
  "packed-varint": WIRE_LENGTH_DELIMITED,
  "packed-zigzag32": WIRE_LENGTH_DELIMITED,
  "packed-zigzag64": WIRE_LENGTH_DELIMITED,
  "packed-fixed32": WIRE_LENGTH_DELIMITED,
  "packed-fixed64": WIRE_LENGTH_DELIMITED,
};

interface CompiledSchema {
  schema: MessageSchema;
  byField: Map<number, FieldSpec>;
}

const compiledCache = new WeakMap<MessageSchema, CompiledSchema>();

export function compileSchema(schema: MessageSchema): CompiledSchema {
  let c = compiledCache.get(schema);
  if (c === undefined) {
    const byField = new Map<number, FieldSpec>();
    for (const f of schema.fields) {
      if (f.kind === "message" && f.schema === undefined) {
        throw new Error(`schema ${schema.name}.${f.name}: message kind needs nested schema`);
      }
      byField.set(f.field, f);
    }
    c = { schema, byField };
    compiledCache.set(schema, c);
  }
  return c;
}

function isPacked(kind: FieldKind): boolean {
  return kind.charCodeAt(0) === 112; // 'p' — packed-*
}

function readScalar(reader: WireReader, kind: FieldKind): unknown {
  switch (kind) {
    case "varint":
      return reader.readVarint();
    case "varint64":
      return reader.readVarint64();
    case "zigzag32":
      return reader.readZigZag32();
    case "zigzag64":
      return reader.readZigZag64();
    case "fixed32":
      return reader.readFixed32();
    case "fixed64":
      return reader.readFixed64();
    case "bytes":
      return reader.readBytes();
    case "string":
      return reader.readString();
    default:
      throw new Error(`not a scalar kind: ${kind}`);
  }
}

function readPackedInto(
  reader: WireReader,
  kind: FieldKind,
  out: unknown[],
): void {
  const savedEnd = reader.enterSub();
  const scalar = kind.slice(7) as FieldKind; // strip "packed-"
  while (!reader.done) {
    out.push(readScalar(reader, scalar));
  }
  reader.leaveSub(savedEnd);
}

/**
 * Decode one message from the reader according to `schema`.
 * When `target` is supplied it is reused in place (arrays truncated,
 * sub-objects kept) for allocation-free steady-state decoding.
 */
export function decodeMessage(
  reader: WireReader,
  schema: MessageSchema,
  target?: Record<string, unknown>,
): Record<string, unknown> {
  const compiled = compileSchema(schema);
  const out: Record<string, unknown> = target ?? {};

  // Prepare reuse slots (allocation-free once target is warm).
  for (const f of schema.fields) {
    if (f.repeated || isPacked(f.kind)) {
      const existing = out[f.name];
      if (Array.isArray(existing)) existing.length = 0;
      else out[f.name] = [];
    } else if (f.kind === "message") {
      if (typeof out[f.name] !== "object" || out[f.name] === null) {
        out[f.name] = {};
      }
    } else {
      delete out[f.name];
    }
  }

  while (!reader.done) {
    const [fieldNo, wireType] = reader.readTag();
    if (fieldNo === 0) {
      throw new TruncatedFieldError(reader.pos, 0, 0, "invalid field number 0");
    }
    const spec = compiled.byField.get(fieldNo);
    if (spec === undefined) {
      reader.skip(wireType); // unknown field: bounds-checked skip
      continue;
    }
    const expected = EXPECTED_WIRE[spec.kind];
    if (wireType !== expected) {
      throw new WireTypeMismatchError(fieldNo, expected, wireType);
    }

    if (isPacked(spec.kind)) {
      readPackedInto(reader, spec.kind, out[spec.name] as unknown[]);
    } else if (spec.kind === "message") {
      const savedEnd = reader.enterSub();
      if (spec.repeated) {
        const child: Record<string, unknown> = {};
        (out[spec.name] as unknown[]).push(child);
        decodeMessage(reader, spec.schema as MessageSchema, child);
      } else {
        decodeMessage(
          reader,
          spec.schema as MessageSchema,
          out[spec.name] as Record<string, unknown>,
        );
      }
      reader.leaveSub(savedEnd);
    } else if (spec.repeated) {
      (out[spec.name] as unknown[]).push(readScalar(reader, spec.kind));
    } else {
      out[spec.name] = readScalar(reader, spec.kind);
    }
  }
  return out;
}
