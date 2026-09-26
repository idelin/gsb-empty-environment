export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

/** Expression node usable inside a `default: { $expr: ... }`. */
export type Expr =
  | { $get: string }
  | { op: "concat" | "add" | "sub" | "mul" | "div" | "coalesce"; args: ExprArg[] }
  | { op: "literal"; value: JsonValue };

export type ExprArg = Expr | JsonValue;

export type DefaultSpec = JsonValue | { $expr: Expr };

export interface Schema {
  type?: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null";
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  enum?: JsonValue[];
  const?: JsonValue;
  default?: DefaultSpec;
}

export interface Issue {
  /** JSON Pointer into the instance, e.g. "/items/0/name". */
  instancePath: string;
  /** "#"-prefixed JSON Pointer into the schema, e.g. "#/properties/items/items/properties/name/type". */
  schemaPath: string;
  keyword: string;
  message: string;
}

export type Result =
  | { ok: true; value: JsonValue }
  | { ok: false; errors: Issue[] };

export function isPlainObject(v: unknown): v is JsonObject {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function escapePointerSegment(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

export function joinPointer(base: string, seg: string): string {
  return base + "/" + escapePointerSegment(seg);
}

export function deepClone<T extends JsonValue>(v: T): T {
  return structuredClone(v);
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  if (isPlainObject(a) || isPlainObject(b)) {
    if (!isPlainObject(a) || !isPlainObject(b)) return false;
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    return ka.every((k) => Object.hasOwn(b, k) && deepEqual(a[k], b[k]));
  }
  return false;
}
