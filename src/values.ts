/**
 * Value model with explicit missing semantics.
 *
 * A missing value is the *absence* of a key/index. It is never conflated
 * with null, 0, false, or the empty list, and it is never zero-filled.
 */

export type LookupResult =
  | { readonly present: true; readonly value: unknown }
  | { readonly present: false };

export function lookup(root: unknown, segments: readonly string[]): LookupResult {
  let current: unknown = root;
  for (const segment of segments) {
    if (current === null || typeof current !== "object") return { present: false };
    if (Array.isArray(current)) {
      if (!/^(0|[1-9]\d*)$/.test(segment)) return { present: false };
      const index = Number(segment);
      if (index >= current.length) return { present: false };
      current = current[index];
    } else {
      if (!Object.prototype.hasOwnProperty.call(current, segment)) return { present: false };
      current = (current as Record<string, unknown>)[segment];
    }
  }
  return { present: true, value: current };
}

export type TypeTag = "int" | "number" | "string" | "bool" | "list" | "object" | "null" | "other";

export function typeOf(value: unknown): TypeTag {
  if (value === null) return "null";
  if (Array.isArray(value)) return "list";
  switch (typeof value) {
    case "number":
      return Number.isInteger(value) ? "int" : "number";
    case "string":
      return "string";
    case "boolean":
      return "bool";
    case "object":
      return "object";
    default:
      return "other";
  }
}

/** Three-valued truth: relations over missing operands are "unknown", never silently false. */
export type Tri = "true" | "false" | "unknown";

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((item, i) => deepEqual(item, b[i]))
    );
  }
  if (typeof a === "object" && typeof b === "object") {
    const aRec = a as Record<string, unknown>;
    const bRec = b as Record<string, unknown>;
    const aKeys = Object.keys(aRec);
    const bKeys = Object.keys(bRec);
    return (
      aKeys.length === bKeys.length &&
      aKeys.every(
        (key) =>
          Object.prototype.hasOwnProperty.call(bRec, key) && deepEqual(aRec[key], bRec[key]),
      )
    );
  }
  return false;
}
