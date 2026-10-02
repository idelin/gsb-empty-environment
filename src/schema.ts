/**
 * Schema-driven struct codec: reflection-style mapping between protobuf
 * wire fields and plain JS objects. Supports nested length-delimited
 * messages and packed repeated scalar arrays.
 */
import { WireType, WireError } from './wire.ts';
import { WireReader } from './reader.ts';
import { WireWriter } from './writer.ts';

export type ScalarType = 'varint' | 'zigzag' | 'fixed32' | 'fixed64' | 'string' | 'bytes' | 'bool';

export interface FieldDef {
  name: string;
  number: number;
  type: ScalarType;
  repeated?: boolean;
  packed?: boolean;
  /** Nested message schema (wire type must be Len). */
  message?: Schema;
  /** Identity key of this entity (used by the Inspector). */
  key?: boolean;
  /** This field's value references the key of another entity. */
  ref?: { entity: string };
  /** Children of this repeated message field are graph roots for orphan analysis. */
  root?: boolean;
}

export interface Schema {
  name: string;
  fields: FieldDef[];
}

export interface UnknownField {
  path: string;
  field: number;
  wire: number;
}

export interface Leak {
  path: string;
  unconsumed: number;
}

export interface DecodeStats {
  unknownFields: UnknownField[];
  leaks: Leak[];
}

export function createStats(): DecodeStats {
  return { unknownFields: [], leaks: [] };
}

const fieldMapCache = new WeakMap<Schema, Map<number, FieldDef>>();

function fieldMap(schema: Schema): Map<number, FieldDef> {
  let map = fieldMapCache.get(schema);
  if (!map) {
    map = new Map();
    for (const f of schema.fields) map.set(f.number, f);
    fieldMapCache.set(schema, map);
  }
  return map;
}

function scalarWireType(t: ScalarType): number {
  switch (t) {
    case 'varint':
    case 'zigzag':
    case 'bool':
      return WireType.Varint;
    case 'fixed32':
      return WireType.I32;
    case 'fixed64':
      return WireType.I64;
    case 'string':
    case 'bytes':
      return WireType.Len;
  }
}

function readScalar(reader: WireReader, t: ScalarType): unknown {
  switch (t) {
    case 'varint':
      return reader.readVarint();
    case 'zigzag':
      return reader.readZigZag();
    case 'bool':
      return reader.readVarint() !== 0n;
    case 'fixed32':
      return reader.readFixed32();
    case 'fixed64':
      return reader.readFixed64();
    case 'string':
      return reader.readString();
    case 'bytes':
      return reader.readBytes();
  }
}

function assign(out: Record<string, unknown>, def: FieldDef, value: unknown): void {
  if (def.repeated) {
    (out[def.name] as unknown[]).push(value);
  } else {
    out[def.name] = value;
  }
}

function readField(
  reader: WireReader,
  def: FieldDef,
  wire: number,
  out: Record<string, unknown>,
  stats?: DecodeStats,
): void {
  const path = `${reader.path}.${def.name}`;
  if (def.message) {
    if (wire !== WireType.Len) {
      throw new WireError(`${path}: nested message expects wire type 2 (LEN), got ${wire}`);
    }
    const sub = reader.subReader(reader.readLength(), path);
    const value = decodeStruct(def.message, sub, stats, path);
    if (!sub.atEnd()) stats?.leaks.push({ path, unconsumed: sub.remaining });
    assign(out, def, value);
    return;
  }
  if (def.repeated && def.packed && wire === WireType.Len) {
    const sub = reader.subReader(reader.readLength(), path);
    const arr = out[def.name] as unknown[];
    while (!sub.atEnd()) arr.push(readScalar(sub, def.type));
    return;
  }
  const expected = scalarWireType(def.type);
  if (wire !== expected) {
    throw new WireError(`${path}: wire type mismatch, expected ${expected}, got ${wire}`);
  }
  assign(out, def, readScalar(reader, def.type));
}

/** Decode a full message. Unknown fields are skipped safely and recorded in stats. */
export function decodeStruct(
  schema: Schema,
  input: Uint8Array | WireReader,
  stats?: DecodeStats,
  path = '$',
): Record<string, unknown> {
  const reader = input instanceof WireReader ? input : new WireReader(input, undefined, path);
  const out: Record<string, unknown> = {};
  const map = fieldMap(schema);
  for (const f of schema.fields) {
    if (f.repeated) out[f.name] = [];
  }
  while (!reader.atEnd()) {
    const { field, wire } = reader.readTag();
    const def = map.get(field);
    if (!def) {
      stats?.unknownFields.push({ path: reader.path, field, wire });
      reader.skip(wire);
      continue;
    }
    readField(reader, def, wire, out, stats);
  }
  return out;
}

function writeScalar(writer: WireWriter, t: ScalarType, value: unknown): void {
  switch (t) {
    case 'varint':
      writer.writeVarint(value as number | bigint);
      return;
    case 'zigzag':
      writer.writeZigZag(value as number | bigint);
      return;
    case 'bool':
      writer.writeVarint(value ? 1 : 0);
      return;
    case 'fixed32':
      writer.writeFixed32(value as number);
      return;
    case 'fixed64':
      writer.writeFixed64(value as number | bigint);
      return;
    case 'string':
      writer.writeString(value as string);
      return;
    case 'bytes':
      writer.writeBytes(value as Uint8Array);
      return;
  }
}

function writeOne(writer: WireWriter, def: FieldDef, value: unknown): void {
  if (def.message) {
    const bytes = encodeStruct(def.message, value as Record<string, unknown>);
    writer.writeTag(def.number, WireType.Len).writeBytes(bytes);
    return;
  }
  writer.writeTag(def.number, scalarWireType(def.type));
  writeScalar(writer, def.type, value);
}

/** Encode an object according to the schema. Packed repeated fields emit a single LEN record. */
export function encodeStruct(
  schema: Schema,
  obj: Record<string, unknown>,
  writer = new WireWriter(),
): Uint8Array {
  for (const def of schema.fields) {
    const value = obj[def.name];
    if (value === undefined || value === null) continue;
    if (def.repeated) {
      const arr = value as unknown[];
      if (def.packed && !def.message) {
        if (arr.length === 0) continue;
        const inner = new WireWriter();
        for (const item of arr) writeScalar(inner, def.type, item);
        writer.writeTag(def.number, WireType.Len).writeBytes(inner.toBytes());
      } else {
        for (const item of arr) writeOne(writer, def, item);
      }
    } else {
      writeOne(writer, def, value);
    }
  }
  return writer.toBytes();
}
