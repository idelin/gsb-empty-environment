import { DecodeError } from '../wire/errors.ts';
import { BufferReader } from '../wire/reader.ts';
import {
  WireType,
  isPackable,
  wireTypeOf,
  type FieldDescriptor,
  type MessageDescriptor,
} from './schema.ts';

export const DEFAULT_MAX_DEPTH = 100;

export interface DecodeContext {
  maxDepth: number;
  onUnknownField?: (fieldNumber: number, wireType: number) => void;
}

/** Compiled fast lookup of a descriptor, cached per descriptor object. */
export class CompiledSchema {
  readonly byNumber: Map<number, FieldDescriptor>;
  readonly descriptor: MessageDescriptor;
  constructor(descriptor: MessageDescriptor) {
    this.descriptor = descriptor;
    this.byNumber = new Map();
    for (const f of descriptor.fields) {
      if (this.byNumber.has(f.number)) {
        throw new Error(`duplicate field number ${f.number} in ${descriptor.name}`);
      }
      this.byNumber.set(f.number, f);
    }
  }
}

function readScalar(reader: BufferReader, field: FieldDescriptor): unknown {
  switch (field.type) {
    case 'int32': return Number(BigInt.asIntN(32, reader.readVarint()));
    case 'int64': return BigInt.asIntN(64, reader.readVarint());
    case 'uint32': return reader.readVarint32();
    case 'uint64': return reader.readVarint();
    case 'sint32': return reader.readSVarint32();
    case 'sint64': return reader.readSVarint64();
    case 'bool': return reader.readVarint() !== 0n;
    case 'fixed32': return reader.readFixed32();
    case 'fixed64': return reader.readFixed64();
    case 'sfixed32': return reader.readSFixed32();
    case 'sfixed64': return reader.readSFixed64();
    case 'float': return reader.readFloat();
    case 'double': return reader.readDouble();
    case 'string': return reader.readString();
    case 'bytes': return reader.readBytes();
    case 'message':
      throw new DecodeError('ERR_INVALID_WIRE_TYPE', 'message type requires nested decode', reader.offset, field.number);
  }
}

function decodePacked(reader: BufferReader, field: FieldDescriptor, out: unknown[]): void {
  const bytes = reader.readBytes();
  const sub = new BufferReader(bytes);
  try {
    while (!sub.done) {
      out.push(readScalar(sub, field));
    }
  } catch (err) {
    if (err instanceof DecodeError && err.fieldNumber === undefined) {
      throw new DecodeError(err.code, `packed field ${field.name}: ${err.detail}`, reader.offset, field.number);
    }
    throw err;
  } finally {
    sub.release();
  }
}

export function decodeMessage(
  reader: BufferReader,
  schema: CompiledSchema,
  ctx: DecodeContext,
  depth: number,
): Record<string, unknown> {
  if (depth > ctx.maxDepth) {
    throw new DecodeError('ERR_DEPTH_LIMIT', `nesting depth exceeds ${ctx.maxDepth}`, reader.offset);
  }
  const out: Record<string, unknown> = {};
  while (!reader.done) {
    const tagOffset = reader.offset;
    const tag = reader.readVarint();
    const wireType = Number(tag & 0x7n);
    const fieldNumber = Number(tag >> 3n);
    if (fieldNumber === 0) {
      throw new DecodeError('ERR_INVALID_TAG', 'field number 0 is not valid', tagOffset);
    }
    const field = schema.byNumber.get(fieldNumber);
    if (!field) {
      ctx.onUnknownField?.(fieldNumber, wireType);
      reader.skip(wireType);
      continue;
    }
    const expected = wireTypeOf(field.type);
    let value: unknown;
    if (field.repeated && field.packed !== false && isPackable(field.type) && wireType === WireType.LengthDelimited) {
      const arr: unknown[] = [];
      try {
        decodePacked(reader, field, arr);
      } catch (err) {
        if (err instanceof DecodeError && err.fieldNumber === undefined) {
          throw new DecodeError(err.code, err.detail, err.offset, fieldNumber);
        }
        throw err;
      }
      value = arr;
    } else if (wireType !== expected) {
      throw new DecodeError(
        'ERR_WIRE_TYPE_MISMATCH',
        `field ${field.name} expects wire type ${expected}, got ${wireType}`,
        tagOffset,
        fieldNumber,
      );
    } else if (field.type === 'message') {
      const bytes = reader.readBytes();
      const sub = new BufferReader(bytes);
      try {
        value = decodeMessage(sub, new CompiledSchema(field.message as MessageDescriptor), ctx, depth + 1);
      } catch (err) {
        if (err instanceof DecodeError && err.fieldNumber === undefined) {
          throw new DecodeError(err.code, err.detail, err.offset, fieldNumber);
        }
        throw err;
      } finally {
        sub.release();
      }
    } else {
      try {
        value = readScalar(reader, field);
      } catch (err) {
        if (err instanceof DecodeError && err.fieldNumber === undefined) {
          throw new DecodeError(err.code, err.detail, err.offset, fieldNumber);
        }
        throw err;
      }
    }
    if (field.repeated) {
      const existing = out[field.name] as unknown[] | undefined;
      if (existing) {
        if (Array.isArray(value)) existing.push(...value);
        else existing.push(value);
      } else {
        out[field.name] = Array.isArray(value) ? value : [value];
      }
    } else {
      out[field.name] = value;
    }
  }
  return out;
}
