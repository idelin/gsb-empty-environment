import { ProtobufDecodeError } from './errors.ts';
import { MAX_FIELD_NUMBER, WireType } from './reader.ts';

export type FieldKind =
  | 'int32'
  | 'int64'
  | 'uint32'
  | 'uint64'
  | 'sint32'
  | 'sint64'
  | 'bool'
  | 'enum'
  | 'fixed32'
  | 'sfixed32'
  | 'float'
  | 'fixed64'
  | 'sfixed64'
  | 'double'
  | 'string'
  | 'bytes'
  | 'message';

export type MessageRef = MessageSchema | (() => MessageSchema);

export interface FieldDescriptor {
  readonly name: string;
  readonly number: number;
  readonly kind: FieldKind;
  readonly repeated?: boolean;
  readonly packed?: boolean;
  readonly required?: boolean;
  readonly message?: MessageRef;
  readonly defaultValue?: unknown;
}

export interface MessageSchema {
  readonly name: string;
  readonly fields: readonly FieldDescriptor[];
  readonly byNumber: ReadonlyMap<number, FieldDescriptor>;
}

const KIND_WIRE_TYPE: Record<FieldKind, number> = {
  int32: WireType.Varint,
  int64: WireType.Varint,
  uint32: WireType.Varint,
  uint64: WireType.Varint,
  sint32: WireType.Varint,
  sint64: WireType.Varint,
  bool: WireType.Varint,
  enum: WireType.Varint,
  fixed64: WireType.Fixed64,
  sfixed64: WireType.Fixed64,
  double: WireType.Fixed64,
  string: WireType.LengthDelimited,
  bytes: WireType.LengthDelimited,
  message: WireType.LengthDelimited,
  fixed32: WireType.Fixed32,
  sfixed32: WireType.Fixed32,
  float: WireType.Fixed32,
};

export function expectedWireType(kind: FieldKind): number {
  return KIND_WIRE_TYPE[kind];
}

export function isPackable(kind: FieldKind): boolean {
  return KIND_WIRE_TYPE[kind] !== WireType.LengthDelimited;
}

export function resolveMessage(ref: MessageRef | undefined): MessageSchema | undefined {
  if (ref === undefined) return undefined;
  return typeof ref === 'function' ? ref() : ref;
}

function fail(message: string): never {
  throw new ProtobufDecodeError('SCHEMA_MISMATCH', message);
}

export function defineSchema(name: string, fields: readonly FieldDescriptor[]): MessageSchema {
  if (!name || typeof name !== 'string') fail('schema requires a non-empty name');
  const byNumber = new Map<number, FieldDescriptor>();
  const names = new Set<string>();
  for (const field of fields) {
    if (!field || typeof field.name !== 'string' || field.name.length === 0) {
      fail(`schema "${name}": every field needs a name`);
    }
    if (!Number.isInteger(field.number) || field.number < 1 || field.number > MAX_FIELD_NUMBER) {
      fail(`schema "${name}": field "${field.name}" has illegal number ${field.number}`);
    }
    if (field.number >= 19000 && field.number <= 19999) {
      fail(`schema "${name}": field "${field.name}" uses reserved number ${field.number}`);
    }
    if (byNumber.has(field.number)) {
      fail(`schema "${name}": duplicate field number ${field.number}`);
    }
    if (names.has(field.name)) {
      fail(`schema "${name}": duplicate field name "${field.name}"`);
    }
    if (!(field.kind in KIND_WIRE_TYPE)) {
      fail(`schema "${name}": field "${field.name}" has unknown kind "${String(field.kind)}"`);
    }
    if (field.kind === 'message') {
      if (field.message === undefined) {
        fail(`schema "${name}": message field "${field.name}" is missing its nested schema`);
      }
    } else if (field.message !== undefined) {
      fail(`schema "${name}": non-message field "${field.name}" must not carry a nested schema`);
    }
    if (field.packed !== undefined) {
      if (!field.repeated) fail(`schema "${name}": field "${field.name}" is packed but not repeated`);
      if (!isPackable(field.kind)) {
        fail(`schema "${name}": field "${field.name}" of kind "${field.kind}" cannot be packed`);
      }
    }
    byNumber.set(field.number, field);
    names.add(field.name);
  }
  const schema: MessageSchema = {
    name,
    fields: Object.freeze([...fields]),
    byNumber,
  };
  return Object.freeze(schema);
}
