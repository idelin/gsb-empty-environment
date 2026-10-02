export const WireType = {
  Varint: 0,
  Fixed64: 1,
  LengthDelimited: 2,
  Fixed32: 5,
} as const;
export type WireTypeValue = (typeof WireType)[keyof typeof WireType];

export type FieldType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'bool'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'float' | 'double'
  | 'string' | 'bytes'
  | 'message';

export interface FieldDescriptor {
  number: number;
  name: string;
  type: FieldType;
  repeated?: boolean;
  /** Repeated scalar numeric fields may arrive packed (wire type 2). */
  packed?: boolean;
  required?: boolean;
  /** Nested descriptor for type === 'message'. */
  message?: MessageDescriptor;
  /** Custom validator; return an issue string or undefined when valid. */
  validate?: (value: unknown) => string | undefined;
}

export interface MessageDescriptor {
  name: string;
  fields: FieldDescriptor[];
}

/** Wire type each scalar field type uses on the wire. */
export function wireTypeOf(type: FieldType): WireTypeValue {
  switch (type) {
    case 'int32': case 'int64': case 'uint32': case 'uint64':
    case 'sint32': case 'sint64': case 'bool':
      return WireType.Varint;
    case 'fixed64': case 'sfixed64': case 'double':
      return WireType.Fixed64;
    case 'fixed32': case 'sfixed32': case 'float':
      return WireType.Fixed32;
    case 'string': case 'bytes': case 'message':
      return WireType.LengthDelimited;
  }
}

export function isPackable(type: FieldType): boolean {
  return type !== 'string' && type !== 'bytes' && type !== 'message';
}
