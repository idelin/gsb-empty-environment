// Core type definitions for the limited schema system.
//
// Supported type kinds (and nothing else):
//   bool, int64, uint64, string (UTF-8), bytes, record, array (bounded).
// Fields are identified by stable numeric IDs; names are display-only.
// Recursive types are impossible by construction: type nodes are inline
// trees and any reference mechanism ($ref/definitions/...) is rejected
// as `unsupported` during schema parsing.

export type TypeNode =
  | { kind: 'bool' }
  | { kind: 'int64'; min: bigint; max: bigint }
  | { kind: 'uint64'; min: bigint; max: bigint }
  | { kind: 'string'; maxLength: number } // length counted in Unicode code points
  | { kind: 'bytes'; maxLength: number } // length counted in bytes
  | { kind: 'array'; items: TypeNode; minLength: number; maxLength: number }
  | { kind: 'record'; fields: Field[] };

export interface Field {
  id: number; // stable numeric identifier (the only identity)
  name?: string; // display only, never used for matching
  required: boolean; // explicit
  hasDefault: boolean; // explicit: whether `defaultValue` was given
  defaultValue?: unknown; // raw JSON, interpreted against `type`
  type: TypeNode;
}

export interface Schema {
  version: number;
  root: TypeNode;
}

// Runtime value model. Records carry an `unknown` side region so that
// fields not present in the schema are preserved, never silently dropped.
export type Value =
  | { kind: 'bool'; value: boolean }
  | { kind: 'int'; value: bigint }
  | { kind: 'string'; value: string }
  | { kind: 'bytes'; value: Uint8Array }
  | { kind: 'array'; items: Value[] }
  | { kind: 'record'; fields: [number, Value][]; unknown: [string, unknown][] };

export const INT64_MIN: bigint = -(1n << 63n);
export const INT64_MAX: bigint = (1n << 63n) - 1n;
export const UINT64_MAX: bigint = (1n << 64n) - 1n;

export function isNumeric(t: TypeNode): t is { kind: 'int64' | 'uint64'; min: bigint; max: bigint } {
  return t.kind === 'int64' || t.kind === 'uint64';
}
