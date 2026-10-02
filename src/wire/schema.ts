import { WireReader, WireType, type WireTypeValue } from "./reader.ts";
import { WireError } from "./errors.ts";

/** Scalar field kinds understood by the reflection decoder. */
export type FieldType =
  | "varint" // uint32/uint64 -> bigint-safe number handling below
  | "int32"
  | "zigzag32"
  | "zigzag64"
  | "fixed32"
  | "sfixed32"
  | "fixed64"
  | "sfixed64"
  | "float"
  | "double"
  | "bool"
  | "bytes"
  | "string"
  | "message";

export interface FieldDef {
  /** Name of the property on the decoded struct. */
  name: string;
  type: FieldType;
  /** Field may appear multiple times; decoded value is always an array. */
  repeated?: boolean;
  /** Repeated scalar encoded in packed (length-delimited) form. */
  packed?: boolean;
  /** Nested schema, required when type === "message". */
  message?: MessageSchema;
}

export interface MessageSchema {
  name?: string;
  /** Keyed by protobuf field number. */
  fields: Record<number, FieldDef>;
}

const WIRE_TYPE_OF: Record<FieldType, WireTypeValue> = {
  varint: WireType.Varint,
  int32: WireType.Varint,
  zigzag32: WireType.Varint,
  zigzag64: WireType.Varint,
  fixed32: WireType.Fixed32,
  sfixed32: WireType.Fixed32,
  fixed64: WireType.Fixed64,
  sfixed64: WireType.Fixed64,
  float: WireType.Fixed32,
  double: WireType.Fixed64,
  bool: WireType.Varint,
  bytes: WireType.LengthDelimited,
  string: WireType.LengthDelimited,
  message: WireType.LengthDelimited,
};

const PACKABLE: ReadonlySet<FieldType> = new Set([
  "varint",
  "int32",
  "zigzag32",
  "zigzag64",
  "fixed32",
  "sfixed32",
  "fixed64",
  "sfixed64",
  "float",
  "double",
  "bool",
]);

function readScalar(reader: WireReader, type: FieldType): unknown {
  switch (type) {
    case "varint":
      return reader.readVarint();
    case "int32":
      return reader.readInt32();
    case "zigzag32":
      return reader.readZigZag32();
    case "zigzag64":
      return reader.readZigZag64();
    case "fixed32":
      return reader.readFixed32();
    case "sfixed32":
      return reader.readSFixed32();
    case "fixed64":
      return reader.readFixed64();
    case "sfixed64":
      return reader.readSFixed64();
    case "float":
      return reader.readFloat();
    case "double":
      return reader.readDouble();
    case "bool":
      return reader.readBool();
    case "bytes":
      return reader.readBytes();
    case "string":
      return reader.readString();
    default:
      throw new WireError("invalid-wire-type", reader.offset, `type ${type} is not a scalar`);
  }
}

function readFieldValue(reader: WireReader, def: FieldDef, wireType: WireTypeValue): unknown {
  if (def.type === "message") {
    if (wireType !== WireType.LengthDelimited || !def.message) {
      throw new WireError("invalid-wire-type", reader.offset, `field ${def.name}: expected LEN for message`);
    }
    return decodeMessage(reader.readSubReader(), def.message);
  }
  const expected = WIRE_TYPE_OF[def.type];
  if (wireType !== expected) {
    throw new WireError(
      "invalid-wire-type",
      reader.offset,
      `field ${def.name}: expected wire type ${expected}, got ${wireType}`,
    );
  }
  return readScalar(reader, def.type);
}

/**
 * Reflection-style decoder: walks the wire stream and materializes a plain
 * struct according to `schema`. Unknown fields are skipped safely. Packed
 * and unpacked encodings of repeated scalars are both accepted. Every read
 * is bounds-checked; truncated input raises `WireError` (kind "truncation").
 */
export function decodeMessage(input: Uint8Array | WireReader, schema: MessageSchema): Record<string, unknown> {
  const reader = input instanceof WireReader ? input : new WireReader(input);
  const out: Record<string, unknown> = {};

  for (const def of Object.values(schema.fields)) {
    if (def.repeated) out[def.name] = [];
  }

  while (!reader.done) {
    const tag = reader.readTag();
    if (tag === null) break;
    const def = schema.fields[tag.field];
    if (!def) {
      reader.skip(tag.wireType);
      continue;
    }

    if (def.repeated && PACKABLE.has(def.type) && tag.wireType === WireType.LengthDelimited) {
      // Packed repeated scalar: a length-delimited run of back-to-back values.
      const sub = reader.readSubReader();
      const list = out[def.name] as unknown[];
      while (!sub.done) list.push(readScalar(sub, def.type));
      continue;
    }

    const value = readFieldValue(reader, def, tag.wireType);
    if (def.repeated) {
      (out[def.name] as unknown[]).push(value);
    } else {
      out[def.name] = value;
    }
  }
  return out;
}
