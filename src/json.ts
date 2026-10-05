/**
 * JSON-compatible value algebra: validation, cloning, structural equality.
 * The store only ever holds values that pass assertJsonValue().
 */

export type JsonObject = { [key: string]: JsonValue };
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | JsonObject;

export type Path = ReadonlyArray<string | number>;

export function isContainer(value: unknown): value is JsonObject | JsonValue[] {
  return typeof value === 'object' && value !== null;
}

/** Throws TypeError unless `value` is a finite, acyclic, plain JSON tree. */
export function assertJsonValue(value: unknown, what = 'value'): asserts value is JsonValue {
  const visiting = new Set<unknown>();
  const check = (node: unknown, where: string): void => {
    if (node === null) return;
    switch (typeof node) {
      case 'boolean':
      case 'string':
        return;
      case 'number':
        if (!Number.isFinite(node)) {
          throw new TypeError(`${what} at ${where}: non-finite number is not JSON-compatible`);
        }
        return;
      case 'object':
        break;
      default:
        throw new TypeError(`${what} at ${where}: ${typeof node} is not JSON-compatible`);
    }
    if (visiting.has(node)) {
      throw new TypeError(`${what} at ${where}: cyclic structure is not JSON-compatible`);
    }
    visiting.add(node);
    if (Array.isArray(node)) {
      for (let i = 0; i < node.length; i += 1) check(node[i], `${where}[${i}]`);
    } else {
      const proto: unknown = Object.getPrototypeOf(node);
      if (proto !== Object.prototype && proto !== null) {
        throw new TypeError(`${what} at ${where}: only plain objects are JSON-compatible`);
      }
      for (const [key, child] of Object.entries(node as JsonObject)) {
        check(child, `${where}.${key}`);
      }
    }
    visiting.delete(node);
  };
  check(value, what);
}

/** Structural deep clone into fresh plain objects/arrays. */
export function cloneJson<T extends JsonValue>(value: T): T {
  if (!isContainer(value)) return value;
  if (Array.isArray(value)) {
    const out: JsonValue[] = new Array(value.length);
    for (let i = 0; i < value.length; i += 1) out[i] = cloneJson(value[i]);
    return out as T;
  }
  const out: JsonObject = {};
  for (const [key, child] of Object.entries(value)) out[key] = cloneJson(child);
  return out as T;
}

/** Structural equality over JSON trees (NaN/Infinity cannot occur post-validation). */
export function jsonEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (!isContainer(a) || !isContainer(b)) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b)) return false;
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i += 1) {
      if (!jsonEqual(a[i], b[i])) return false;
    }
    return true;
  }
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  for (const key of keysA) {
    if (!Object.prototype.hasOwnProperty.call(b, key)) return false;
    if (!jsonEqual(a[key], (b as JsonObject)[key])) return false;
  }
  return true;
}
