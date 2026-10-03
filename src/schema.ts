import { DecodeError } from './wire/errors.ts';
import { WireType } from './wire/types.ts';

export const SCALAR_TYPES = [
  'uint32',
  'uint64',
  'int32',
  'int64',
  'sint32',
  'sint64',
  'bool',
  'fixed32',
  'fixed64',
  'sfixed32',
  'sfixed64',
  'float',
  'double',
  'string',
  'bytes',
] as const;

export type ScalarType = (typeof SCALAR_TYPES)[number];
export type FieldType = ScalarType | 'message';

export const MAX_FIELD_NUMBER = 0x1fffffff; // 2^29 - 1

export interface FieldDescriptor {
  number: number;
  name: string;
  type: FieldType;
  message?: MessageDescriptor;
  repeated?: boolean;
  packed?: boolean;
  required?: boolean;
}

export interface MessageDescriptor {
  name: string;
  fields: readonly FieldDescriptor[];
  byNumber: ReadonlyMap<number, FieldDescriptor>;
  byName: ReadonlyMap<string, FieldDescriptor>;
}

export function isScalarType(type: string): type is ScalarType {
  return (SCALAR_TYPES as readonly string[]).includes(type);
}

export function isPackable(type: FieldType): boolean {
  return type !== 'string' && type !== 'bytes' && type !== 'message';
}

export function wireTypeOf(type: FieldType): WireType {
  switch (type) {
    case 'uint32':
    case 'uint64':
    case 'int32':
    case 'int64':
    case 'sint32':
    case 'sint64':
    case 'bool':
      return WireType.Varint;
    case 'fixed64':
    case 'sfixed64':
    case 'double':
      return WireType.Fixed64;
    case 'fixed32':
    case 'sfixed32':
    case 'float':
      return WireType.Fixed32;
    case 'string':
    case 'bytes':
    case 'message':
      return WireType.LengthDelimited;
    default:
      throw new DecodeError(`no wire type for field type ${type}`);
  }
}

export function defineMessage(name: string, fields: FieldDescriptor[]): MessageDescriptor {
  const byNumber = new Map<number, FieldDescriptor>();
  const byName = new Map<string, FieldDescriptor>();
  for (const field of fields) {
    if (!Number.isInteger(field.number) || field.number < 1 || field.number > MAX_FIELD_NUMBER) {
      throw new DecodeError(
        `message ${name}: field ${field.name} has invalid number ${field.number}`,
      );
    }
    if (byNumber.has(field.number)) {
      throw new DecodeError(
        `message ${name}: duplicate field number ${field.number} (${field.name})`,
      );
    }
    if (byName.has(field.name)) {
      throw new DecodeError(`message ${name}: duplicate field name ${field.name}`);
    }
    if (field.type === 'message' && field.message === undefined) {
      throw new DecodeError(`message ${name}: field ${field.name} is a message without a descriptor`);
    }
    if (field.type !== 'message' && !isScalarType(field.type)) {
      throw new DecodeError(`message ${name}: field ${field.name} has unknown type ${field.type}`);
    }
    if (field.packed) {
      if (!field.repeated) {
        throw new DecodeError(`message ${name}: field ${field.name} is packed but not repeated`);
      }
      if (!isPackable(field.type)) {
        throw new DecodeError(
          `message ${name}: field ${field.name} of type ${field.type} cannot be packed`,
        );
      }
    }
    byNumber.set(field.number, field);
    byName.set(field.name, field);
  }
  return { name, fields, byNumber, byName };
}
