import { WireError } from '../wire/errors.ts';
import {
  WireReader,
  WIRE_VARINT,
  WIRE_I64,
  WIRE_LEN,
  WIRE_I32,
  WIRE_EGROUP,
} from '../wire/reader.ts';
import { zigzagDecode32, zigzagDecode64, toSigned64 } from '../wire/varint.ts';
import { isPackable, type FieldDescriptor, type MessageDescriptor } from './schema.ts';

export interface DecodeOptions {
  maxDepth?: number;
  unknownFields?: 'skip' | 'error';
}

const DEFAULT_MAX_DEPTH = 100;

function expectedWireType(field: FieldDescriptor): number {
  switch (field.type) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
    case 'sint32': case 'sint64': case 'bool': case 'enum':
      return WIRE_VARINT;
    case 'fixed64': case 'sfixed64': case 'double':
      return WIRE_I64;
    case 'fixed32': case 'sfixed32': case 'float':
      return WIRE_I32;
    default:
      return WIRE_LEN;
  }
}

function readScalar(reader: WireReader, type: FieldDescriptor['type']): unknown {
  switch (type) {
    case 'int32': return reader.readVarint32() | 0;
    case 'int64': return toSigned64(reader.readVarint());
    case 'uint32': return reader.readVarint32();
    case 'uint64': return reader.readVarint();
    case 'sint32': return zigzagDecode32(reader.readVarint32());
    case 'sint64': return zigzagDecode64(reader.readVarint());
    case 'bool': return reader.readVarint() !== 0n;
    case 'enum': return reader.readVarint32() | 0;
    case 'fixed32': return reader.readFixed32();
    case 'fixed64': return reader.readFixed64();
    case 'sfixed32': return reader.readFixed32() | 0;
    case 'sfixed64': return toSigned64(reader.readFixed64());
    case 'float': return reader.readFloat();
    case 'double': return reader.readDouble();
    case 'string': return reader.readString();
    case 'bytes': return reader.readBytes();
    default:
      throw new WireError('INVALID_WIRE_TYPE', `type ${type} is not a scalar`, reader.pos);
  }
}

function decodeMessageInto(
  desc: MessageDescriptor,
  reader: WireReader,
  opts: Required<DecodeOptions>,
  depth: number,
  path: string,
): Record<string, unknown> {
  if (depth > opts.maxDepth) {
    throw new WireError(
      'RECURSION_LIMIT',
      `nesting depth exceeds limit of ${opts.maxDepth}`,
      reader.pos,
      path,
    );
  }
  const byNo = new Map(desc.fields.map((f) => [f.no, f]));
  const out: Record<string, unknown> = {};

  while (!reader.done) {
    const tagOffset = reader.pos;
    const tag = reader.readTag();
    if (tag === null) break;
    if (tag.wire === WIRE_EGROUP) {
      throw new WireError('INVALID_WIRE_TYPE', 'unexpected end-of-group tag', tagOffset, path);
    }
    const field = byNo.get(tag.field);
    const fieldPath = path ? `${path}.${field?.name ?? `#${tag.field}`}` : (field?.name ?? `#${tag.field}`);

    if (!field) {
      if (opts.unknownFields === 'error') {
        throw new WireError('UNKNOWN_FIELD', `unknown field ${tag.field}`, tagOffset, fieldPath);
      }
      try {
        reader.skip(tag.wire);
      } catch (err) {
        if (err instanceof WireError && !err.path) {
          throw new WireError(err.code, err.message.replace(/ \(offset .*\)$/, ''), err.offset, fieldPath);
        }
        throw err;
      }
      continue;
    }

    const expected = expectedWireType(field);
    const packedOnWire = field.repeated && isPackable(field.type) && tag.wire === WIRE_LEN;

    if (!packedOnWire && tag.wire !== expected) {
      throw new WireError(
        'WIRE_TYPE_MISMATCH',
        `field "${field.name}" expects wire type ${expected} but found ${tag.wire}`,
        tagOffset,
        fieldPath,
      );
    }

    try {
      if (packedOnWire) {
        const sub = reader.readSubreader();
        const arr = (out[field.name] as unknown[] | undefined) ?? [];
        while (!sub.done) {
          arr.push(readScalar(sub, field.type));
        }
        out[field.name] = arr;
      } else if (field.type === 'message') {
        if (!field.message) {
          throw new WireError('INVALID_TAG', `field "${field.name}" missing message descriptor`, tagOffset, fieldPath);
        }
        const sub = reader.readSubreader();
        const value = decodeMessageInto(field.message, sub, opts, depth + 1, fieldPath);
        if (field.repeated) {
          const arr = (out[field.name] as unknown[] | undefined) ?? [];
          arr.push(value);
          out[field.name] = arr;
        } else {
          out[field.name] = value;
        }
      } else {
        const value = readScalar(reader, field.type);
        if (field.repeated) {
          const arr = (out[field.name] as unknown[] | undefined) ?? [];
          arr.push(value);
          out[field.name] = arr;
        } else {
          out[field.name] = value;
        }
      }
    } catch (err) {
      if (err instanceof WireError && !err.path) {
        throw new WireError(err.code, err.message.replace(/ \(offset .*\)$/, ''), err.offset, fieldPath);
      }
      throw err;
    }
  }
  return out;
}

export function decodeMessage(
  desc: MessageDescriptor,
  bytes: Uint8Array,
  options: DecodeOptions = {},
): Record<string, unknown> {
  const opts: Required<DecodeOptions> = {
    maxDepth: options.maxDepth ?? DEFAULT_MAX_DEPTH,
    unknownFields: options.unknownFields ?? 'skip',
  };
  const reader = new WireReader(bytes);
  return decodeMessageInto(desc, reader, opts, 1, desc.name);
}
