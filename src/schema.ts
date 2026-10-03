import { ProtobufDecodeError, zigzagEncode } from './encoding.ts';
import { ProtoReader, WireType, type WireTypeValue } from './reader.ts';
import { ProtoWriter } from './writer.ts';

export type ScalarType =
  | 'int32'
  | 'int64'
  | 'uint32'
  | 'uint64'
  | 'sint32'
  | 'sint64'
  | 'bool'
  | 'fixed32'
  | 'fixed64'
  | 'sfixed32'
  | 'sfixed64'
  | 'float'
  | 'double'
  | 'string'
  | 'bytes';

export interface FieldDescriptor {
  name: string;
  number: number;
  type: ScalarType | MessageSchema;
  repeated?: boolean;
  packed?: boolean;
  required?: boolean;
}

export class SchemaValidationError extends Error {
  readonly path: string;
  constructor(message: string, path: string) {
    super(`${message} (path=${path})`);
    this.name = 'SchemaValidationError';
    this.path = path;
  }
}

const WIRE_TYPE_OF: Record<ScalarType, WireTypeValue> = {
  int32: WireType.Varint,
  int64: WireType.Varint,
  uint32: WireType.Varint,
  uint64: WireType.Varint,
  sint32: WireType.Varint,
  sint64: WireType.Varint,
  bool: WireType.Varint,
  fixed32: WireType.Fixed32,
  sfixed32: WireType.Fixed32,
  float: WireType.Fixed32,
  fixed64: WireType.Fixed64,
  sfixed64: WireType.Fixed64,
  double: WireType.Fixed64,
  string: WireType.LengthDelimited,
  bytes: WireType.LengthDelimited,
};

const PACKABLE = new Set<ScalarType>([
  'int32',
  'int64',
  'uint32',
  'uint64',
  'sint32',
  'sint64',
  'bool',
  'fixed32',
  'sfixed32',
  'float',
  'fixed64',
  'sfixed64',
  'double',
]);

function isMessage(type: ScalarType | MessageSchema): type is MessageSchema {
  return type instanceof MessageSchema;
}

function expectedWireType(type: ScalarType | MessageSchema): WireTypeValue {
  return isMessage(type) ? WireType.LengthDelimited : WIRE_TYPE_OF[type];
}

function readScalar(reader: ProtoReader, type: ScalarType): unknown {
  switch (type) {
    case 'int32':
      return Number(BigInt.asIntN(32, reader.readVarint()));
    case 'int64':
      return reader.readInt64();
    case 'uint32':
      return reader.readVarint32();
    case 'uint64':
      return reader.readVarint();
    case 'sint32':
      return Number(BigInt.asIntN(32, BigInt(reader.readSVarint())));
    case 'sint64':
      return reader.readSVarint();
    case 'bool':
      return reader.readBool();
    case 'fixed32':
      return reader.readFixed32();
    case 'sfixed32':
      return reader.readFixed32() | 0;
    case 'float':
      return reader.readFloat();
    case 'fixed64':
      return reader.readFixed64();
    case 'sfixed64':
      return BigInt.asIntN(64, reader.readFixed64());
    case 'double':
      return reader.readDouble();
    case 'string':
      return reader.readString();
    case 'bytes':
      return reader.readBytes();
  }
}

function writeScalar(writer: ProtoWriter, type: ScalarType, value: unknown): void {
  switch (type) {
    case 'int32':
    case 'uint32':
      writer.writeVarint(BigInt(value as number) & 0xffffffffn);
      return;
    case 'int64':
    case 'uint64':
      writer.writeVarint(value as bigint);
      return;
    case 'sint32':
      writer.writeVarint(zigzagEncode(BigInt(value as number)) & 0xffffffffn);
      return;
    case 'sint64':
      writer.writeVarint(zigzagEncode(value as bigint));
      return;
    case 'bool':
      writer.writeVarint((value as boolean) ? 1 : 0);
      return;
    case 'fixed32':
    case 'sfixed32':
      writer.writeFixed32((value as number) >>> 0);
      return;
    case 'float':
      writer.writeFloat(value as number);
      return;
    case 'fixed64':
    case 'sfixed64':
      writer.writeFixed64(value as bigint);
      return;
    case 'double':
      writer.writeDouble(value as number);
      return;
    case 'string':
      writer.writeString(value as string);
      return;
    case 'bytes':
      writer.writeBytes(value as Uint8Array);
      return;
  }
}

function checkScalarType(type: ScalarType, value: unknown, path: string): void {
  const fail = (want: string) => {
    throw new SchemaValidationError(
      `expected ${want}, got ${typeof value}`,
      path,
    );
  };
  switch (type) {
    case 'int64':
    case 'uint64':
    case 'sint64':
    case 'fixed64':
    case 'sfixed64':
      if (typeof value !== 'bigint') fail('bigint');
      return;
    case 'bool':
      if (typeof value !== 'boolean') fail('boolean');
      return;
    case 'string':
      if (typeof value !== 'string') fail('string');
      return;
    case 'bytes':
      if (!(value instanceof Uint8Array)) fail('Uint8Array');
      return;
    default:
      if (typeof value !== 'number' || !Number.isFinite(value as number)) {
        fail('finite number');
      }
  }
}

export class MessageSchema {
  readonly name: string;
  readonly fields: readonly FieldDescriptor[];
  private readonly byNumber: Map<number, FieldDescriptor>;

  constructor(name: string, fields: FieldDescriptor[]) {
    this.name = name;
    this.fields = fields;
    this.byNumber = new Map();
    for (const f of fields) {
      if (f.number <= 0 || !Number.isInteger(f.number)) {
        throw new SchemaValidationError(
          `invalid field number ${f.number}`,
          `${name}.${f.name}`,
        );
      }
      if (this.byNumber.has(f.number)) {
        throw new SchemaValidationError(
          `duplicate field number ${f.number}`,
          `${name}.${f.name}`,
        );
      }
      if (f.packed && (isMessage(f.type) || !PACKABLE.has(f.type))) {
        throw new SchemaValidationError(
          `field type is not packable`,
          `${name}.${f.name}`,
        );
      }
      if (f.packed && !f.repeated) {
        throw new SchemaValidationError(
          'packed requires repeated',
          `${name}.${f.name}`,
        );
      }
      this.byNumber.set(f.number, f);
    }
  }

  decode(bytes: Uint8Array): Record<string, unknown> {
    const reader = new ProtoReader(bytes);
    return this.decodeFrom(reader, this.name);
  }

  private decodeFrom(
    reader: ProtoReader,
    path: string,
  ): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    const seen = new Set<string>();
    while (!reader.eof) {
      const tag = reader.readTag();
      if (tag === null) break;
      const field = this.byNumber.get(tag.fieldNumber);
      if (!field) {
        reader.skip(tag.wireType);
        continue;
      }
      const fieldPath = `${path}.${field.name}`;
      try {
        this.readField(reader, field, tag.wireType, out, fieldPath);
      } catch (err) {
        if (err instanceof ProtobufDecodeError) {
          throw new ProtobufDecodeError(
            `${err.message} while decoding ${fieldPath}`,
            err.offset,
          );
        }
        throw err;
      }
      seen.add(field.name);
    }
    for (const field of this.fields) {
      if (field.required && !seen.has(field.name)) {
        throw new SchemaValidationError(
          'required field missing',
          `${path}.${field.name}`,
        );
      }
    }
    return out;
  }

  private readField(
    reader: ProtoReader,
    field: FieldDescriptor,
    wireType: WireTypeValue,
    out: Record<string, unknown>,
    path: string,
  ): void {
    const expected = expectedWireType(field.type);
    const scalar = isMessage(field.type) ? null : field.type;

    if (
      field.repeated &&
      scalar !== null &&
      PACKABLE.has(scalar) &&
      wireType === WireType.LengthDelimited
    ) {
      const sub = reader.subReader();
      const arr = this.arrayFor(out, field.name);
      while (!sub.eof) arr.push(readScalar(sub, scalar));
      return;
    }

    if (wireType !== expected) {
      throw new SchemaValidationError(
        `wire type mismatch: expected ${expected}, got ${wireType}`,
        path,
      );
    }

    if (isMessage(field.type)) {
      const sub = reader.subReader();
      const value = field.type.decodeFrom(sub, path);
      if (field.repeated) this.arrayFor(out, field.name).push(value);
      else out[field.name] = value;
      return;
    }

    const value = readScalar(reader, scalar as ScalarType);
    if (field.repeated) this.arrayFor(out, field.name).push(value);
    else out[field.name] = value;
  }

  private arrayFor(out: Record<string, unknown>, name: string): unknown[] {
    let arr = out[name] as unknown[] | undefined;
    if (!arr) {
      arr = [];
      out[name] = arr;
    }
    return arr;
  }

  encode(obj: Record<string, unknown>): Uint8Array {
    const writer = new ProtoWriter();
    this.encodeInto(writer, obj, this.name);
    return writer.finish();
  }

  private encodeInto(
    writer: ProtoWriter,
    obj: Record<string, unknown>,
    path: string,
  ): void {
    for (const field of this.fields) {
      const fieldPath = `${path}.${field.name}`;
      const value = obj[field.name];
      if (value === undefined || value === null) {
        if (field.required) {
          throw new SchemaValidationError('required field missing', fieldPath);
        }
        continue;
      }
      if (field.repeated) {
        if (!Array.isArray(value)) {
          throw new SchemaValidationError('expected array', fieldPath);
        }
        this.encodeRepeated(writer, field, value, fieldPath);
      } else {
        this.encodeSingle(writer, field, value, fieldPath);
      }
    }
  }

  private encodeRepeated(
    writer: ProtoWriter,
    field: FieldDescriptor,
    values: unknown[],
    path: string,
  ): void {
    const scalar = isMessage(field.type) ? null : field.type;
    if (field.packed && scalar !== null) {
      const inner = new ProtoWriter();
      values.forEach((v, i) => {
        checkScalarType(scalar, v, `${path}[${i}]`);
        writeScalar(inner, scalar, v);
      });
      writer.writeTag(field.number, WireType.LengthDelimited);
      writer.writeBytes(inner.finish());
      return;
    }
    values.forEach((v, i) => this.encodeSingle(writer, field, v, `${path}[${i}]`));
  }

  private encodeSingle(
    writer: ProtoWriter,
    field: FieldDescriptor,
    value: unknown,
    path: string,
  ): void {
    if (isMessage(field.type)) {
      if (typeof value !== 'object' || value === null) {
        throw new SchemaValidationError('expected object', path);
      }
      const inner = new ProtoWriter();
      field.type.encodeInto(inner, value as Record<string, unknown>, path);
      writer.writeTag(field.number, WireType.LengthDelimited);
      writer.writeBytes(inner.finish());
      return;
    }
    checkScalarType(field.type, value, path);
    writer.writeTag(field.number, WIRE_TYPE_OF[field.type]);
    writeScalar(writer, field.type, value);
  }
}
