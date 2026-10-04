import { WireReader, WireType, type WireTypeValue } from './wire/reader.ts';
import { zigzagDecode, toSigned64, toSigned32 } from './wire/varint.ts';
import { ProtobufDecodeError, SchemaValidationError } from './wire/errors.ts';

/** Scalar kinds understood by the reflection engine. */
export type ScalarType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'bool'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'string' | 'bytes' | 'enum';

export interface FieldDescriptor {
  name: string;
  number: number;
  type: ScalarType | 'message';
  /** Nested message schema, required when type === 'message'. */
  messageType?: MessageSchema;
  repeated?: boolean;
  /** Proto3 packed repeated scalar. Only meaningful for numeric scalars. */
  packed?: boolean;
  required?: boolean;
}

export interface MessageSchema {
  name: string;
  fields: FieldDescriptor[];
}

export interface DecodeOptions {
  /** When true (default), unknown fields are collected instead of rejected. */
  allowUnknownFields?: boolean;
  /** Maximum nesting depth for embedded messages (default 32). */
  maxDepth?: number;
}

export interface DecodedMessage {
  [field: string]: unknown;
}

const WIRE_TYPE_BY_SCALAR: Record<string, WireTypeValue> = {
  int32: WireType.Varint, int64: WireType.Varint,
  uint32: WireType.Varint, uint64: WireType.Varint,
  sint32: WireType.Varint, sint64: WireType.Varint,
  bool: WireType.Varint, enum: WireType.Varint,
  fixed32: WireType.Fixed32, sfixed32: WireType.Fixed32,
  fixed64: WireType.Fixed64, sfixed64: WireType.Fixed64,
  string: WireType.LengthDelimited, bytes: WireType.LengthDelimited,
};

const PACKABLE = new Set([
  'int32', 'int64', 'uint32', 'uint64', 'sint32', 'sint64',
  'bool', 'enum', 'fixed32', 'sfixed32', 'fixed64', 'sfixed64',
]);

function coerceScalar(type: ScalarType, wireType: WireTypeValue, reader: WireReader): unknown {
  switch (type) {
    case 'int32': return toSigned32(Number(reader.readVarint() & 0xffffffffn));
    case 'int64': return toSigned64(reader.readVarint());
    case 'uint32': return Number(reader.readVarint() & 0xffffffffn);
    case 'uint64': return reader.readVarint();
    case 'sint32': return Number(zigzagDecode(reader.readVarint()) & 0xffffffffn) | 0;
    case 'sint64': return zigzagDecode(reader.readVarint());
    case 'bool': return reader.readVarint() !== 0n;
    case 'enum': return toSigned32(Number(reader.readVarint() & 0xffffffffn));
    case 'fixed32': return reader.readFixed32();
    case 'sfixed32': return toSigned32(reader.readFixed32());
    case 'fixed64': return reader.readFixed64();
    case 'sfixed64': return toSigned64(reader.readFixed64());
    case 'string': {
      const bytes = reader.readBytes();
      try {
        return new TextDecoder('utf-8', { fatal: true }).decode(bytes);
      } catch {
        throw new ProtobufDecodeError('invalid UTF-8 in string field', reader.offset);
      }
    }
    case 'bytes': return reader.readBytes();
    default:
      throw new ProtobufDecodeError(`cannot coerce wire type ${wireType} as ${type}`, reader.offset);
  }
}

function readPackedScalar(type: ScalarType, payload: Uint8Array): unknown[] {
  const reader = new WireReader(payload);
  const out: unknown[] = [];
  while (!reader.eof) {
    out.push(coerceScalar(type, WIRE_TYPE_BY_SCALAR[type], reader));
  }
  return out;
}

/**
 * Reflection-style engine: validates a schema descriptor up front, then
 * decodes a binary stream into a typed structure, checking wire-type
 * compatibility, required fields, nesting depth, and packed arrays.
 */
export class ReflectionEngine {
  private readonly schemas = new Map<string, MessageSchema>();

  registerSchema(schema: MessageSchema): void {
    this.validateSchema(schema, schema.name, new Set());
    this.schemas.set(schema.name, schema);
  }

  getSchema(name: string): MessageSchema | undefined {
    return this.schemas.get(name);
  }

  private validateSchema(schema: MessageSchema, path: string, visiting: Set<string>): void {
    if (visiting.has(schema.name)) {
      throw new SchemaValidationError(schema.name, [`recursive schema cycle at ${path}`]);
    }
    visiting.add(schema.name);
    const seen = new Map<number, string>();
    const issues: string[] = [];
    for (const f of schema.fields) {
      if (!Number.isInteger(f.number) || f.number < 1 || f.number > 0x1fffffff) {
        issues.push(`field ${f.name}: number ${f.number} out of range [1, 2^29-1]`);
      }
      if (f.number >= 19000 && f.number <= 19999) {
        issues.push(`field ${f.name}: number ${f.number} is in the reserved range 19000-19999`);
      }
      const prev = seen.get(f.number);
      if (prev !== undefined) {
        issues.push(`field ${f.name}: number ${f.number} duplicates field ${prev}`);
      }
      seen.set(f.number, f.name);
      if (f.type === 'message') {
        if (!f.messageType) {
          issues.push(`field ${f.name}: message type requires messageType schema`);
        } else {
          this.validateSchema(f.messageType, `${path}.${f.name}`, visiting);
        }
      } else if (f.packed && !PACKABLE.has(f.type)) {
        issues.push(`field ${f.name}: type ${f.type} cannot be packed`);
      }
    }
    visiting.delete(schema.name);
    if (issues.length > 0) throw new SchemaValidationError(schema.name, issues);
  }

  decode(buf: Uint8Array, schema: MessageSchema, options: DecodeOptions = {}): DecodedMessage {
    const allowUnknown = options.allowUnknownFields ?? true;
    const maxDepth = options.maxDepth ?? 32;
    const unknownFields: Array<{ fieldNumber: number; wireType: WireTypeValue }> = [];
    const result = this.decodeMessage(new WireReader(buf), schema, unknownFields, maxDepth, 0);
    if (!allowUnknown && unknownFields.length > 0) {
      throw new SchemaValidationError(
        schema.name,
        unknownFields.map((u) => `unknown field ${u.fieldNumber} (wire type ${u.wireType})`),
      );
    }
    if (unknownFields.length > 0) result.$unknownFields = unknownFields;
    return result;
  }

  private decodeMessage(
    reader: WireReader,
    schema: MessageSchema,
    unknownFields: Array<{ fieldNumber: number; wireType: WireTypeValue }>,
    maxDepth: number,
    depth: number,
  ): DecodedMessage {
    if (depth > maxDepth) {
      throw new ProtobufDecodeError(`nesting depth exceeds limit of ${maxDepth}`, reader.offset);
    }
    const byNumber = new Map<number, FieldDescriptor>();
    for (const f of schema.fields) byNumber.set(f.number, f);
    const out: DecodedMessage = {};
    const seen = new Set<string>();

    while (!reader.eof) {
      const { fieldNumber, wireType } = reader.readTag();
      const field = byNumber.get(fieldNumber);
      if (!field) {
        // Record and skip; strict mode reports them after the message is done.
        unknownFields.push({ fieldNumber, wireType });
        reader.skipField(wireType);
        continue;
      }

      if (field.type === 'message') {
        if (wireType !== WireType.LengthDelimited) {
          throw new ProtobufDecodeError(
            `field ${field.name}: expected length-delimited wire type, got ${wireType}`,
            reader.offset,
          );
        }
        const payload = reader.readBytes();
        const nested = this.decodeMessage(
          new WireReader(payload),
          field.messageType as MessageSchema,
          unknownFields,
          maxDepth,
          depth + 1,
        );
        if (field.repeated) {
          (out[field.name] as unknown[] ??= []).push(nested);
        } else {
          out[field.name] = nested;
        }
        seen.add(field.name);
        continue;
      }

      const expected = WIRE_TYPE_BY_SCALAR[field.type];
      if (field.packed && wireType === WireType.LengthDelimited) {
        const payload = reader.readBytes();
        out[field.name] = readPackedScalar(field.type as ScalarType, payload);
        seen.add(field.name);
        continue;
      }
      if (wireType !== expected) {
        throw new ProtobufDecodeError(
          `field ${field.name}: expected wire type ${expected}, got ${wireType}`,
          reader.offset,
        );
      }
      const value = coerceScalar(field.type as ScalarType, wireType, reader);
      if (field.repeated) {
        (out[field.name] as unknown[] ??= []).push(value);
      } else {
        out[field.name] = value;
      }
      seen.add(field.name);
    }

    const missing = schema.fields
      .filter((f) => f.required && !seen.has(f.name))
      .map((f) => `missing required field ${f.name}`);
    if (missing.length > 0) throw new SchemaValidationError(schema.name, missing);
    return out;
  }
}
