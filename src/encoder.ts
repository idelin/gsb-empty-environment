/**
 * Schema-driven encoder. Encoding is not the latency-critical path in this
 * project, so nested messages use a fresh scratch writer per level for
 * clarity; the top-level writer is caller-reusable.
 */
import { WireWriter, WireType, DecodeError } from './wire.ts';
import type { FieldDef, Schema } from './decoder.ts';

export function encode(schema: Schema, msg: Record<string, unknown>, writer = new WireWriter()): Uint8Array {
  writer.reset();
  writeMessage(schema, msg, writer);
  return writer.finish();
}

function writeMessage(schema: Schema, msg: Record<string, unknown>, writer: WireWriter): void {
  for (const def of schema.fields) {
    const value = msg[def.name];
    if (value === undefined || value === null) continue;
    if (def.repeated) {
      const items = value as unknown[];
      if (def.packed) {
        if (items.length === 0) continue;
        const scratch = new WireWriter();
        for (const item of items) writeScalar(def, item, scratch);
        writer.writeTag(def.no, WireType.LengthDelimited);
        writer.writeBytes(scratch.finish());
      } else {
        for (const item of items) writeField(def, item, writer);
      }
    } else {
      writeField(def, value, writer);
    }
  }
}

function writeField(def: FieldDef, value: unknown, writer: WireWriter): void {
  if (def.type === 'message') {
    const scratch = new WireWriter();
    writeMessage(def.schema as Schema, value as Record<string, unknown>, scratch);
    writer.writeTag(def.no, WireType.LengthDelimited);
    writer.writeBytes(scratch.finish());
    return;
  }
  writer.writeTag(def.no, wireOf(def));
  writeScalar(def, value, writer);
}

function wireOf(def: FieldDef): number {
  switch (def.type) {
    case 'varint':
    case 'zigzag32':
    case 'zigzag64':
    case 'bool':
      return WireType.Varint;
    case 'fixed64':
      return WireType.Fixed64;
    case 'fixed32':
      return WireType.Fixed32;
    case 'string':
    case 'bytes':
      return WireType.LengthDelimited;
    default:
      throw new DecodeError(`field ${def.name}: type ${def.type} has no scalar wire type`);
  }
}

function writeScalar(def: FieldDef, value: unknown, writer: WireWriter): void {
  switch (def.type) {
    case 'varint':
      writer.writeVarint(value as number | bigint);
      return;
    case 'zigzag32':
      writer.writeZigZag32(value as number);
      return;
    case 'zigzag64':
      writer.writeZigZag64(value as bigint);
      return;
    case 'fixed32':
      writer.writeFixed32(value as number);
      return;
    case 'fixed64':
      writer.writeFixed64(value as bigint);
      return;
    case 'bool':
      writer.writeBool(value as boolean);
      return;
    case 'string':
      writer.writeString(value as string);
      return;
    case 'bytes':
      writer.writeBytes(value as Uint8Array);
      return;
    default:
      throw new DecodeError(`field ${def.name}: cannot encode type ${def.type} as scalar`);
  }
}
