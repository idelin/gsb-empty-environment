import {
  deepEqual,
  effectiveEnum,
  escapePointer,
  has,
  valueTypes,
  type JsonSchema,
  type SchemaObject,
  type ValueType,
} from "./schema.ts";
import { intervalContains, lenInterval, numInterval, sizeInterval } from "./intervals.ts";

export function classify(v: unknown): ValueType {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  switch (typeof v) {
    case "boolean":
      return "boolean";
    case "string":
      return "string";
    case "number":
      return Number.isInteger(v) ? "integer" : "number";
    default:
      return "object";
  }
}

/** String length in code points (JSON Schema semantics). */
export function stringLength(s: string): number {
  return Array.from(s).length;
}

/** Does the schema accept the value? (Supported subset only.) */
export function accepts(schema: JsonSchema, v: unknown): boolean {
  if (schema === true) return true;
  if (schema === false) return false;
  return acceptsObject(schema, v, false);
}

/** Accepts ignoring the enum/const keywords (used to filter enum values). */
export function acceptsNonEnum(s: SchemaObject, v: unknown): boolean {
  return acceptsObject(s, v, true);
}

function acceptsObject(s: SchemaObject, v: unknown, skipEnum: boolean): boolean {
  const vt = classify(v);
  if (!valueTypes(s).has(vt)) return false;
  if (!skipEnum) {
    const en = effectiveEnum(s);
    if (en !== undefined && !en.some((e) => deepEqual(e, v))) return false;
  }
  if (vt === "integer" || vt === "number") {
    if (!intervalContains(numInterval(s), v as number)) return false;
  }
  if (vt === "string") {
    if (!intervalContains(lenInterval(s), stringLength(v as string))) return false;
  }
  if (vt === "array") {
    const arr = v as unknown[];
    if (!intervalContains(sizeInterval(s), arr.length)) return false;
    const items: JsonSchema = s.items ?? true;
    for (const item of arr) if (!accepts(items, item)) return false;
  }
  if (vt === "object") {
    const obj = v as Record<string, unknown>;
    for (const r of s.required ?? []) if (!has(obj, r)) return false;
    const props = s.properties ?? {};
    for (const [k, val] of Object.entries(obj)) {
      if (has(props, k)) {
        if (!accepts(props[k], val)) return false;
      } else {
        const ap = s.additionalProperties;
        if (ap === false) return false;
        if (ap !== undefined && !accepts(ap, val)) return false;
      }
    }
  }
  return true;
}

/**
 * Path (JSON Pointer into the schema) of the first keyword that rejects `v`.
 * Assumes the schema does reject `v`.
 */
export function explainReject(schema: JsonSchema, v: unknown, path: string): string {
  if (schema === false) return (path === "" ? "" : path) + "#false";
  if (schema === true) return path;
  const s = schema;
  const vt = classify(v);
  if (!valueTypes(s).has(vt)) return path + "/type";
  const en = effectiveEnum(s);
  if (en !== undefined && !en.some((e) => deepEqual(e, v))) {
    return path + (s.const !== undefined && s.enum === undefined ? "/const" : "/enum");
  }
  if (vt === "integer" || vt === "number") {
    const x = v as number;
    const iv = numInterval(s);
    if (iv.min !== undefined && (x < iv.min || (x === iv.min && iv.minExcl === true))) {
      return path + (typeof s.exclusiveMinimum === "number" && iv.min === s.exclusiveMinimum
        ? "/exclusiveMinimum"
        : "/minimum");
    }
    if (iv.max !== undefined && (x > iv.max || (x === iv.max && iv.maxExcl === true))) {
      return path + (typeof s.exclusiveMaximum === "number" && iv.max === s.exclusiveMaximum
        ? "/exclusiveMaximum"
        : "/maximum");
    }
  }
  if (vt === "string") {
    const len = stringLength(v as string);
    if (s.minLength !== undefined && len < s.minLength) return path + "/minLength";
    if (s.maxLength !== undefined && len > s.maxLength) return path + "/maxLength";
  }
  if (vt === "array") {
    const arr = v as unknown[];
    if (s.minItems !== undefined && arr.length < s.minItems) return path + "/minItems";
    if (s.maxItems !== undefined && arr.length > s.maxItems) return path + "/maxItems";
    const items: JsonSchema = s.items ?? true;
    for (let i = 0; i < arr.length; i++) {
      if (!accepts(items, arr[i])) return explainReject(items, arr[i], path + "/items");
    }
  }
  if (vt === "object") {
    const obj = v as Record<string, unknown>;
    for (const r of s.required ?? []) if (!has(obj, r)) return path + "/required";
    const props = s.properties ?? {};
    for (const [k, val] of Object.entries(obj)) {
      if (has(props, k)) {
        if (!accepts(props[k], val)) {
          return explainReject(props[k], val, path + "/properties/" + escapePointer(k));
        }
      } else {
        const ap = s.additionalProperties;
        if (ap === false) return path + "/additionalProperties";
        if (ap !== undefined && !accepts(ap, val)) {
          return explainReject(ap, val, path + "/additionalProperties");
        }
      }
    }
  }
  return path;
}
