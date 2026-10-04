// Core type model. Types contain only: bool, int64, uint64, UTF-8 string,
// bytes, nested record, finite array. Fields use stable numeric IDs; names
// are display-only. Recursive types are impossible by construction (types
// are inline trees; any $ref-like key is rejected as unsupported).

export type TypeNode =
  | { kind: 'bool' }
  | { kind: 'int64'; min: bigint; max: bigint }
  | { kind: 'uint64'; min: bigint; max: bigint }
  | { kind: 'string'; maxLen: number } // maxLen counts Unicode code points
  | { kind: 'bytes'; maxLen: number } // maxLen counts bytes
  | { kind: 'array'; element: TypeNode; maxLen: number }
  | { kind: 'record'; fields: Field[] };

export type RecordType = { kind: 'record'; fields: Field[] };

export interface Field {
  id: number; // stable numeric ID, unique within the record
  name: string; // display only, never used for compatibility
  required: boolean; // explicit
  hasDefault: boolean; // defaults are only allowed on optional fields
  defaultValue: Value | undefined; // normalized
  type: TypeNode;
}

// Normalized value model. Integers are bigint, bytes are Uint8Array,
// records are Rec (known fields plus a bag of unknown fields preserved
// verbatim). Missing is represented by absence, never null.
export type Value = boolean | bigint | string | Uint8Array | Value[] | Rec;

export class Rec {
  fields: Record<string, Value>;
  unknown: Record<string, unknown>;
  constructor(fields: Record<string, Value>, unknown: Record<string, unknown>) {
    this.fields = fields;
    this.unknown = unknown;
  }
}
