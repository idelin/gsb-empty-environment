import { DepIndex, type DepMode } from "./depindex.ts";
import { escapeSegment, formatPointer, parsePointer, type Segments } from "./paths.ts";

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

export interface CrossRule {
  kind: "lt" | "lte" | "eq";
  /** Absolute JSON pointer into the instance, e.g. "/x". */
  left: string;
  right: string;
}

/** The supported schema subset. Anything not listed here is rejected nowhere
 *  but simply not promised: unknown keywords are ignored by design. */
export interface SchemaNode {
  type?: "object" | "array" | "string" | "number" | "integer" | "boolean" | "null";
  properties?: Record<string, SchemaNode>;
  required?: string[];
  items?: SchemaNode;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
  enum?: Json[];
  /** Cross-field rules. Only honoured on the root schema node. */
  rules?: CrossRule[];
}

export interface ValidationError {
  /** JSON pointer into the instance document. */
  instancePath: string;
  /** JSON pointer into the schema ("rule path"), e.g. "#/properties/x/minimum". */
  schemaPath: string;
  code: string;
  message: string;
}

export interface Dep {
  segs: Segments;
  mode: DepMode;
}

export interface Check {
  id: number;
  schemaPath: string;
  deps: Dep[];
  evaluate(root: Json): ValidationError[];
}

const WILDCARD = "*";

/** Instance location of a check; may contain WILDCARD for array elements. */
type Location = Segments;

type Emit = (
  schemaPath: string,
  deps: Dep[],
  evaluate: (root: Json) => ValidationError[],
) => void;

export function compileSchema(schema: SchemaNode): { checks: Check[]; index: DepIndex } {
  const checks: Check[] = [];
  const emit: Emit = (schemaPath, deps, evaluate) => {
    checks.push({ id: checks.length, schemaPath, deps: dedupeDeps(deps), evaluate });
  };
  compileNode(schema, [], "#", emit);
  (schema.rules ?? []).forEach((rule, i) => compileRule(rule, `#/rules/${i}`, emit));
  const index = new DepIndex();
  for (const check of checks) {
    for (const dep of check.deps) index.add(dep.segs, dep.mode, check.id);
  }
  return { checks, index };
}

function compileNode(s: SchemaNode, location: Location, schemaPath: string, emit: Emit): void {
  if (s.type !== undefined) {
    const expected = s.type;
    const sp = `${schemaPath}/type`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => !matchesType(r.value, expected))
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "type",
          message: `expected ${expected}, got ${typeOf(r.value)}`,
        })),
    );
  }
  if (s.minimum !== undefined) {
    const min = s.minimum;
    const sp = `${schemaPath}/minimum`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => typeof r.value === "number" && r.value < min)
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "minimum",
          message: `must be >= ${min}`,
        })),
    );
  }
  if (s.maximum !== undefined) {
    const max = s.maximum;
    const sp = `${schemaPath}/maximum`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => typeof r.value === "number" && r.value > max)
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "maximum",
          message: `must be <= ${max}`,
        })),
    );
  }
  if (s.minLength !== undefined) {
    const min = s.minLength;
    const sp = `${schemaPath}/minLength`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => typeof r.value === "string" && r.value.length < min)
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "minLength",
          message: `string shorter than ${min}`,
        })),
    );
  }
  if (s.maxLength !== undefined) {
    const max = s.maxLength;
    const sp = `${schemaPath}/maxLength`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => typeof r.value === "string" && r.value.length > max)
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "maxLength",
          message: `string longer than ${max}`,
        })),
    );
  }
  if (s.pattern !== undefined) {
    const re = new RegExp(s.pattern);
    const sp = `${schemaPath}/pattern`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => typeof r.value === "string" && !re.test(r.value))
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "pattern",
          message: `must match /${s.pattern}/`,
        })),
    );
  }
  if (s.enum !== undefined) {
    const allowed = s.enum;
    const sp = `${schemaPath}/enum`;
    emit(sp, depsFor(location), (root) =>
      resolve(root, location)
        .filter((r) => !allowed.some((v) => deepEqual(v, r.value)))
        .map((r) => ({
          instancePath: formatPointer(r.segs),
          schemaPath: sp,
          code: "enum",
          message: "value not in enum",
        })),
    );
  }
  for (const key of s.required ?? []) {
    const sp = `${schemaPath}/required`;
    // The outcome depends on the object itself and on the presence of `key`.
    const deps = [...depsFor(location), ...depsFor([...location, key])];
    emit(sp, deps, (root) => {
      const errors: ValidationError[] = [];
      for (const r of resolve(root, location)) {
        if (isPlainObject(r.value) && !Object.prototype.hasOwnProperty.call(r.value, key)) {
          errors.push({
            instancePath: formatPointer(r.segs),
            schemaPath: sp,
            code: "required",
            message: `missing required property "${key}"`,
          });
        }
      }
      return errors;
    });
  }
  for (const [key, sub] of Object.entries(s.properties ?? {})) {
    compileNode(sub, [...location, key], `${schemaPath}/properties/${escapeSegment(key)}`, emit);
  }
  if (s.items !== undefined) {
    compileNode(s.items, [...location, WILDCARD], `${schemaPath}/items`, emit);
  }
}

function compileRule(rule: CrossRule, schemaPath: string, emit: Emit): void {
  const left = parsePointer(rule.left);
  const right = parsePointer(rule.right);
  const symbol = rule.kind === "lt" ? "<" : rule.kind === "lte" ? "<=" : "==";
  const deps: Dep[] = [
    { segs: left, mode: "node" },
    { segs: right, mode: "node" },
  ];
  emit(schemaPath, deps, (root) => {
    const l = resolve(root, left);
    const r = resolve(root, right);
    // A rule is not applicable while one of its operands is absent.
    if (l.length === 0 || r.length === 0) return [];
    const lv = l[0].value;
    const rv = r[0].value;
    if (rule.kind === "eq") {
      return deepEqual(lv, rv)
        ? []
        : [{ instancePath: rule.left, schemaPath, code: "rule", message: `expected ${rule.left} == ${rule.right}` }];
    }
    if (typeof lv !== "number" || typeof rv !== "number") {
      return [{
        instancePath: rule.left,
        schemaPath,
        code: "ruleOperands",
        message: `rule operands must be numbers (${rule.left} ${symbol} ${rule.right})`,
      }];
    }
    const ok = rule.kind === "lt" ? lv < rv : lv <= rv;
    return ok
      ? []
      : [{ instancePath: rule.left, schemaPath, code: "rule", message: `expected ${rule.left} ${symbol} ${rule.right}` }];
  });
}

interface Resolved {
  segs: Segments;
  value: Json;
}

/** Expands a (possibly wildcarded) location against the document. */
function resolve(root: Json, location: Location): Resolved[] {
  const out: Resolved[] = [];
  const walk = (value: Json, rest: Segments, acc: Segments): void => {
    if (rest.length === 0) {
      out.push({ segs: acc, value });
      return;
    }
    const [head, ...tail] = rest;
    if (head === WILDCARD) {
      if (Array.isArray(value)) {
        value.forEach((item, i) => walk(item, tail, [...acc, String(i)]));
      }
      return;
    }
    if (isPlainObject(value) && Object.prototype.hasOwnProperty.call(value, head)) {
      walk(value[head], tail, [...acc, head]);
    } else if (Array.isArray(value) && isArrayIndex(head) && Number(head) < value.length) {
      walk(value[Number(head)], tail, [...acc, head]);
    }
  };
  walk(root, location, []);
  return out;
}

/** Dependency of checks at `location`: the concrete prefix up to the first
 *  wildcard; anything past a wildcard makes it a subtree dependency. */
function depsFor(location: Location): Dep[] {
  const concrete: Segments = [];
  for (const seg of location) {
    if (seg === WILDCARD) break;
    concrete.push(seg);
  }
  const hasWildcard = concrete.length !== location.length;
  return [{ segs: concrete, mode: hasWildcard ? "subtree" : "node" }];
}

function dedupeDeps(deps: Dep[]): Dep[] {
  const byPath = new Map<string, Dep>();
  for (const dep of deps) {
    const key = JSON.stringify(dep.segs);
    const prev = byPath.get(key);
    // "subtree" subsumes "node" for the same path.
    if (prev === undefined || (prev.mode === "node" && dep.mode === "subtree")) {
      byPath.set(key, dep);
    }
  }
  return [...byPath.values()];
}

export function isPlainObject(value: Json | undefined): value is Record<string, Json> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function isArrayIndex(segment: string): boolean {
  return /^(0|[1-9]\d*)$/.test(segment);
}

function typeOf(value: Json): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function matchesType(value: Json, expected: NonNullable<SchemaNode["type"]>): boolean {
  switch (expected) {
    case "null": return value === null;
    case "boolean": return typeof value === "boolean";
    case "string": return typeof value === "string";
    case "number": return typeof value === "number";
    case "integer": return typeof value === "number" && Number.isInteger(value);
    case "array": return Array.isArray(value);
    case "object": return isPlainObject(value);
  }
}

function deepEqual(a: Json, b: Json): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((v, i) => deepEqual(v, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    return ka.length === kb.length && ka.every((k) =>
      Object.prototype.hasOwnProperty.call(b, k) && deepEqual(a[k], b[k]));
  }
  return false;
}
