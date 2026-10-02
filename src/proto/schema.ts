export type ScalarType =
  | 'int32' | 'int64' | 'uint32' | 'uint64'
  | 'sint32' | 'sint64'
  | 'bool' | 'enum'
  | 'fixed32' | 'fixed64' | 'sfixed32' | 'sfixed64'
  | 'float' | 'double'
  | 'string' | 'bytes';

export type FieldType = ScalarType | 'message';

export interface FieldDescriptor {
  no: number;
  name: string;
  type: FieldType;
  repeated?: boolean;
  packed?: boolean;
  message?: MessageDescriptor;
}

export interface MessageDescriptor {
  name: string;
  fields: FieldDescriptor[];
}

export function isPackable(type: FieldType): boolean {
  return type !== 'string' && type !== 'bytes' && type !== 'message';
}
