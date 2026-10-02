import { WireWriter } from "./writer.ts";
import { WireType } from "./reader.ts";
import type { FieldDef, MessageSchema } from "./schema.ts";

function writeScalar(w: WireWriter, type: FieldDef["type"], value: unknown): void {
  switch (type) {
    case "varint":
      w.writeVarint(value as bigint | number);
      return;
    case "int32":
      w.writeVarint(BigInt.asUintN(64, BigInt(value as number)));
      return;
    case "zigzag32":
      w.writeZigZag32(value as number);
      return;
    case "zigzag64":
      w.writeZigZag64(BigInt(value as bigint | number));
      return;
    case "fixed32":
    case "sfixed32":
      w.writeFixed32(value as number);
      return;
    case "fixed64":
    case "sfixed64":
      w.writeFixed64(BigInt(value as bigint | number));
      return;
    case "float":
      w.writeFloat(value as number);
      return;
    case "double":
      w.writeDouble(value as number);
      return;
    case "bool":
      w.writeVarint(value ? 1 : 0);
      return;
    case "bytes":
      w.writeBytes(value as Uint8Array);
      return;
    case "string":
      w.writeString(value as string);
      return;
    default:
      throw new Error(`cannot encode scalar of type ${type}`);
  }
}

const SCALAR_WIRE: Record<string, number> = {
  varint: WireType.Varint,
  int32: WireType.Varint,
  zigzag32: WireType.Varint,
  zigzag64: WireType.Varint,
  bool: WireType.Varint,
  fixed32: WireType.Fixed32,
  sfixed32: WireType.Fixed32,
  float: WireType.Fixed32,
  fixed64: WireType.Fixed64,
  sfixed64: WireType.Fixed64,
  double: WireType.Fixed64,
  bytes: WireType.LengthDelimited,
  string: WireType.LengthDelimited,
};

/** Schema-driven encoder: the inverse of `decodeMessage`. */
export function encodeMessage(obj: Record<string, unknown>, schema: MessageSchema): Uint8Array {
  const w = new WireWriter();
  for (const [fieldKey, def] of Object.entries(schema.fields)) {
    const field = Number(fieldKey);
    const value = obj[def.name];
    if (value === undefined || value === null) continue;

    if (def.repeated) {
      const items = value as unknown[];
      if (def.packed && items.length > 0) {
        const inner = new WireWriter();
        for (const item of items) writeScalar(inner, def.type, item);
        w.writeTag(field, WireType.LengthDelimited);
        w.writeBytes(inner.finish());
      } else {
        for (const item of items) encodeOne(w, field, def, item);
      }
      continue;
    }
    encodeOne(w, field, def, value);
  }
  return w.finish();
}

function encodeOne(w: WireWriter, field: number, def: FieldDef, value: unknown): void {
  if (def.type === "message") {
    if (!def.message) throw new Error(`field ${def.name}: missing nested schema`);
    const payload = encodeMessage(value as Record<string, unknown>, def.message);
    w.writeTag(field, WireType.LengthDelimited);
    w.writeBytes(payload);
    return;
  }
  w.writeTag(field, SCALAR_WIRE[def.type]);
  writeScalar(w, def.type, value);
}
