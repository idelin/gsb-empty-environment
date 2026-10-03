import { DecodeError } from './wire/errors.ts';
import { WireReader } from './wire/reader.ts';
import { WireWriter } from './wire/writer.ts';
import { WireType, wireTypeName } from './wire/types.ts';
import { UINT32_MASK, UINT64_MASK, zigzagDecode32, zigzagDecode64, zigzagEncode32, zigzagEncode64 } from './wire/varint.ts';
import { isPackable, wireTypeOf } from './schema.ts';
import type { FieldDescriptor, MessageDescriptor } from './schema.ts';
import { assertValid } from './validate.ts';
import type { ValidateOptions } from './validate.ts';

export interface DecodeOptions {
  maxDepth?: number;
  allowUnknownFields?: boolean;
}

const DEFAULT_MAX_DEPTH = 64;
const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

function toSigned32(u: bigint): number {
  const v = Number(u & UINT32_MASK);
  return v >= 0x80000000 ? v - 0x100000000 : v;
}

function toSigned64(u: bigint): bigint {
  const v = u & UINT64_MASK;
  return v >= 1n << 63n ? v - (1n << 64n) : v;
}

function readScalar(reader: WireReader, field: FieldDescriptor): unknown {
  switch (field.type) {
    case 'uint32':
      return Number(reader.readVarint() & UINT32_MASK);
    case 'uint64':
      return reader.readVarint() & UINT64_MASK;
    case 'int32':
      return toSigned32(reader.readVarint());
    case 'int64':
      return toSigned64(reader.readVarint());
    case 'sint32':
      return zigzagDecode32(Number(reader.readVarint() & UINT32_MASK));
    case 'sint64':
      return zigzagDecode64(reader.readVarint());
    case 'bool':
      return reader.readVarint() !== 0n;
    case 'fixed32':
      return reader.readFixed32();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed32':
      return toSigned32(BigInt(reader.readFixed32()));
    case 'sfixed64':
      return toSigned64(reader.readFixed64());
    case 'float':
      return reader.readFloat();
    case 'double':
      return reader.readDouble();
    case 'string':
      return textDecoder.decode(reader.readLengthDelimited());
    case 'bytes':
      return reader.readLengthDelimited();
    default:
      throw new DecodeError(`cannot read scalar of type ${field.type}`);
  }
}

function decodeInto(
  desc: MessageDescriptor,
  reader: WireReader,
  opts: Required<DecodeOptions>,
  depth: number,
  out: Record<string, unknown>,
): void {
  if (depth > opts.maxDepth) {
    throw new DecodeError(`message ${desc.name}: nesting depth exceeds limit ${opts.maxDepth}`);
  }
  while (!reader.done) {
    const tag = reader.readTag();
    const field = desc.byNumber.get(tag.fieldNumber);
    if (field === undefined) {
      if (!opts.allowUnknownFields) {
        throw new DecodeError(
          `message ${desc.name}: unknown field number ${tag.fieldNumber} at offset ${reader.pos}`,
        );
      }
      reader.skipField(tag.wireType);
      continue;
    }
    if (field.type === 'message') {
      if (tag.wireType !== WireType.LengthDelimited) {
        throw new DecodeError(
          `message ${desc.name}: field ${field.name} expects LengthDelimited, got ${wireTypeName(tag.wireType)}`,
        );
      }
      const sub = reader.readSubreader();
      const nested: Record<string, unknown> = {};
      decodeInto(field.message as MessageDescriptor, sub, opts, depth + 1, nested);
      if (!sub.done) {
        throw new DecodeError(`message ${desc.name}: field ${field.name} has trailing bytes`);
      }
      if (field.repeated) {
        (out[field.name] as unknown[] ?? (out[field.name] = [])).push(nested);
      } else {
        out[field.name] = nested;
      }
      continue;
    }
    const expectedWire = wireTypeOf(field.type);
    if (
      field.repeated &&
      isPackable(field.type) &&
      tag.wireType === WireType.LengthDelimited
    ) {
      const sub = reader.readSubreader();
      const items: unknown[] = (out[field.name] as unknown[]) ?? (out[field.name] = []);
      while (!sub.done) {
        items.push(readScalar(sub, field));
      }
      continue;
    }
    if (tag.wireType !== expectedWire) {
      throw new DecodeError(
        `message ${desc.name}: field ${field.name} expects wire type ${wireTypeName(expectedWire)}, got ${wireTypeName(tag.wireType)}`,
      );
    }
    const value = readScalar(reader, field);
    if (field.repeated) {
      (out[field.name] as unknown[] ?? (out[field.name] = [])).push(value);
    } else {
      out[field.name] = value;
    }
  }
}

export function decodeMessage(
  desc: MessageDescriptor,
  buf: Uint8Array,
  opts: DecodeOptions = {},
): Record<string, unknown> {
  const resolved: Required<DecodeOptions> = {
    maxDepth: opts.maxDepth ?? DEFAULT_MAX_DEPTH,
    allowUnknownFields: opts.allowUnknownFields ?? true,
  };
  const reader = WireReader.of(buf);
  const out: Record<string, unknown> = {};
  decodeInto(desc, reader, resolved, 0, out);
  return out;
}

function writeScalar(writer: WireWriter, field: FieldDescriptor, value: unknown): void {
  switch (field.type) {
    case 'uint32':
      writer.writeVarint(BigInt(value as number) & UINT32_MASK);
      return;
    case 'uint64':
      writer.writeVarint(BigInt(value as number | bigint) & UINT64_MASK);
      return;
    case 'int32':
    case 'int64':
      writer.writeVarint(BigInt(value as number | bigint) & UINT64_MASK);
      return;
    case 'sint32':
      writer.writeVarint(BigInt(zigzagEncode32(value as number)));
      return;
    case 'sint64':
      writer.writeVarint(zigzagEncode64(BigInt(value as number | bigint)));
      return;
    case 'bool':
      writer.writeVarint((value as boolean) ? 1n : 0n);
      return;
    case 'fixed32':
      writer.writeFixed32(value as number);
      return;
    case 'fixed64':
      writer.writeFixed64(BigInt(value as number | bigint));
      return;
    case 'sfixed32':
      writer.writeFixed32((value as number) >>> 0);
      return;
    case 'sfixed64':
      writer.writeFixed64(BigInt(value as number | bigint) & UINT64_MASK);
      return;
    case 'float':
      writer.writeFloat(value as number);
      return;
    case 'double':
      writer.writeDouble(value as number);
      return;
    case 'string':
      writer.writeLengthDelimited(textEncoder.encode(value as string));
      return;
    case 'bytes':
      writer.writeLengthDelimited(value as Uint8Array);
      return;
    default:
      throw new DecodeError(`cannot write scalar of type ${field.type}`);
  }
}

function encodeField(writer: WireWriter, field: FieldDescriptor, value: unknown): void {
  if (field.type === 'message') {
    const nested = encodeMessage(field.message as MessageDescriptor, value as Record<string, unknown>);
    writer.writeTag(field.number, WireType.LengthDelimited);
    writer.writeLengthDelimited(nested);
    return;
  }
  writer.writeTag(field.number, wireTypeOf(field.type));
  writeScalar(writer, field, value);
}

export function encodeMessage(
  desc: MessageDescriptor,
  value: Record<string, unknown>,
  opts: ValidateOptions = {},
): Uint8Array {
  assertValid(desc, value, opts);
  const writer = new WireWriter();
  for (const field of desc.fields) {
    const fieldValue = value[field.name];
    if (fieldValue === undefined || fieldValue === null) continue;
    if (field.repeated) {
      const items = fieldValue as unknown[];
      if (items.length === 0) continue;
      if (field.packed) {
        const packed = new WireWriter();
        for (const item of items) writeScalar(packed, field, item);
        writer.writeTag(field.number, WireType.LengthDelimited);
        writer.writeLengthDelimited(packed.finish());
      } else {
        for (const item of items) encodeField(writer, field, item);
      }
    } else {
      encodeField(writer, field, fieldValue);
    }
  }
  return writer.finish();
}
