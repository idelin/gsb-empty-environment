/**
 * Versioned schema subset. Only the constructs declared here are supported;
 * anything outside this subset is intentionally not promised.
 *
 * - scalar: "string" | "number" | "boolean"
 * - ref: a single ID reference to a document of `target` type
 * - refArray: an array of ID references to documents of `target` type
 * - object: nested inline object with its own properties/required
 * - TypeDef.maxIncoming: reverse-cardinality constraint, the maximum number
 *   of inbound references (counted per reference site) a document of this
 *   type may receive.
 *
 * Documents may carry extra properties not listed in `properties`; they are
 * ignored by validation (additional properties are allowed).
 */
export type ScalarKind = "string" | "number" | "boolean";

export type PropDef =
  | { kind: "scalar"; scalar: ScalarKind }
  | { kind: "ref"; target: string }
  | { kind: "refArray"; target: string }
  | { kind: "object"; properties: Record<string, PropDef>; required?: string[] };

export interface TypeDef {
  properties: Record<string, PropDef>;
  required?: string[];
  maxIncoming?: number;
}

export interface Schema {
  version: number;
  types: Record<string, TypeDef>;
}
