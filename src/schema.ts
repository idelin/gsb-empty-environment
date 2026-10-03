/**
 * The supported contract (schema) subset. Only these keywords are promised:
 *
 *  - type: "null" | "boolean" | "integer" | "number" | "string" | "array" | "object"
 *          (single name or array of names)
 *  - enum / const            (finite enumeration of allowed values)
 *  - minimum / maximum / exclusiveMinimum / exclusiveMaximum   (numeric intervals)
 *  - minLength / maxLength   (string length, integer interval, code points)
 *  - minItems / maxItems     (array size, integer interval)
 *  - items                   (single subschema applied to every element)
 *  - properties / required / additionalProperties
 *  - boolean schemas `true` (accept all) and `false` (accept nothing)
 *
 * Anything else (pattern, format, allOf/anyOf/oneOf/not, $ref, multipleOf,
 * minProperties, patternProperties, ...) is reported as UNSUPPORTED and the
 * verdict becomes "unknown" instead of "compatible": we never claim
 * compatibility we cannot prove.
 */
export type JsonSchema = boolean | SchemaObject;

export interface SchemaObject {
  type?: string | string[];
  enum?: unknown[];
  const?: unknown;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number | boolean;
  exclusiveMaximum?: number | boolean;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  items?: JsonSchema;
  properties?: Record<string, JsonSchema>;
  required?: string[];
  additionalProperties?: JsonSchema;
}

/** "number" means a non-integer number here; "integer" is a separate value type. */
export type ValueType = "null" | "boolean" | "integer" | "number" | "string" | "array" | "object";

export const ALL_VALUE_TYPES: readonly ValueType[] = [
  "null",
  "boolean",
  "integer",
  "number",
  "string",
  "array",
  "object",
];

export const SUPPORTED_KEYWORDS: ReadonlySet<string> = new Set([
  "type",
  "enum",
  "const",
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "minLength",
  "maxLength",
  "minItems",
  "maxItems",
  "items",
  "properties",
  "required",
  "additionalProperties",
]);

const VALID_TYPE_NAMES: ReadonlySet<string> = new Set(ALL_VALUE_TYPES);

export interface UnsupportedReason {
  rulePath: string;
  message: string;
}

export function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function has(obj: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(obj, key);
}

export function escapePointer(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** Deep equality for JSON values (object key order insensitive, 0 === -0). */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (typeof a === "number" && typeof b === "number") return a === b;
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((x, i) => deepEqual(x, (b as unknown[])[i]));
  }
  if (typeof a === "object") {
    const ao = a as Record<string, unknown>;
    const bo = b as Record<string, unknown>;
    const ak = Object.keys(ao);
    const bk = Object.keys(bo);
    if (ak.length !== bk.length) return false;
    return ak.every((k) => has(bo, k) && deepEqual(ao[k], bo[k]));
  }
  return false;
}

/** Deterministic stringify with sorted object keys, used for deduplication. */
export function stableStringify(v: unknown): string {
  if (Array.isArray(v)) return "[" + v.map(stableStringify).join(",") + "]";
  if (v !== null && typeof v === "object") {
    const parts = Object.entries(v as Record<string, unknown>)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
      .map(([k, val]) => JSON.stringify(k) + ":" + stableStringify(val));
    return "{" + parts.join(",") + "}";
  }
  const s = JSON.stringify(v);
  return s === undefined ? "undefined" : s;
}

/** Set of value types allowed by the `type` keyword (absent = all). */
export function valueTypes(s: SchemaObject): Set<ValueType> {
  if (s.type === undefined) return new Set(ALL_VALUE_TYPES);
  const names = Array.isArray(s.type) ? s.type : [s.type];
  const out = new Set<ValueType>();
  for (const n of names) {
    if (n === "number") {
      out.add("integer");
      out.add("number");
    } else {
      out.add(n as ValueType);
    }
  }
  return out;
}

/** Effective finite enumeration: `const` folded into `enum` (intersection). */
export function effectiveEnum(s: SchemaObject): unknown[] | undefined {
  let vals: unknown[] | undefined;
  if (s.enum !== undefined) vals = s.enum;
  if (s.const !== undefined) {
    vals = vals === undefined ? [s.const] : vals.filter((v) => deepEqual(v, s.const));
  }
  return vals;
}

/**
 * Walk a schema and collect every use of a keyword or shape outside the
 * supported subset. Presence of any reason means the verdict must be
 * "unknown" (or, at best, "breaking" if a definite counterexample exists).
 */
export function findUnsupported(
  schema: JsonSchema,
  path: string,
  out: UnsupportedReason[] = [],
): UnsupportedReason[] {
  if (typeof schema === "boolean") return out;
  if (!isPlainObject(schema)) {
    out.push({ rulePath: path === "" ? "/" : path, message: "schema must be a boolean or an object" });
    return out;
  }
  for (const key of Object.keys(schema)) {
    if (!SUPPORTED_KEYWORDS.has(key)) {
      out.push({ rulePath: path + "/" + escapePointer(key), message: `unsupported keyword "${key}"` });
    }
  }
  const s = schema as SchemaObject;
  if (s.type !== undefined) {
    const names = Array.isArray(s.type) ? s.type : [s.type];
    if (
      names.length === 0 ||
      !names.every((n) => typeof n === "string" && VALID_TYPE_NAMES.has(n))
    ) {
      out.push({
        rulePath: path + "/type",
        message: '"type" must be a JSON type name or a non-empty array of names',
      });
    }
  }
  if (s.enum !== undefined && !Array.isArray(s.enum)) {
    out.push({ rulePath: path + "/enum", message: '"enum" must be an array' });
  }
  for (const k of ["minimum", "maximum"] as const) {
    if (s[k] !== undefined && (typeof s[k] !== "number" || !Number.isFinite(s[k] as number))) {
      out.push({ rulePath: path + "/" + k, message: `"${k}" must be a finite number` });
    }
  }
  for (const k of ["exclusiveMinimum", "exclusiveMaximum"] as const) {
    const v = s[k];
    if (v === undefined) continue;
    if (typeof v === "number" && Number.isFinite(v)) continue;
    if (typeof v === "boolean") continue;
    out.push({ rulePath: path + "/" + k, message: `"${k}" must be a finite number or a boolean` });
  }
  for (const k of ["minLength", "maxLength", "minItems", "maxItems"] as const) {
    const v = s[k];
    if (v !== undefined && (typeof v !== "number" || !Number.isInteger(v) || v < 0)) {
      out.push({ rulePath: path + "/" + k, message: `"${k}" must be a non-negative integer` });
    }
  }
  if (
    s.required !== undefined &&
    (!Array.isArray(s.required) || !s.required.every((r) => typeof r === "string"))
  ) {
    out.push({ rulePath: path + "/required", message: '"required" must be an array of strings' });
  }
  if (s.properties !== undefined && !isPlainObject(s.properties)) {
    out.push({ rulePath: path + "/properties", message: '"properties" must be an object' });
  }
  if (s.items !== undefined) findUnsupported(s.items, path + "/items", out);
  if (isPlainObject(s.properties)) {
    for (const [k, sub] of Object.entries(s.properties)) {
      findUnsupported(sub as JsonSchema, path + "/properties/" + escapePointer(k), out);
    }
  }
  if (s.additionalProperties !== undefined) {
    findUnsupported(s.additionalProperties, path + "/additionalProperties", out);
  }
  return out;
}
