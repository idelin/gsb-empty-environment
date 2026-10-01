import { makeError, type ValidationError } from "./errors.ts";

/**
 * The promised schema subset. Anything outside these node shapes is rejected
 * at registration time (we only promise what we implement):
 *
 *   { kind: "string" | "number" | "integer" | "boolean" | "null" }
 *   { kind: "enum", values: unknown[] }
 *   { kind: "array", items: TypeNode, minItems?, maxItems? }
 *   { kind: "object", properties: {..}, required?: string[] }
 *   { kind: "ref", targets: string[], maxIncoming?: number }
 *
 * A `ref` value in document data is a string: the id of another document.
 * `targets` lists the allowed type names of the referenced document.
 * `maxIncoming` is the reverse cardinality: at most that many distinct
 * documents may reference the same target through this rule node.
 */
export type TypeNode =
  | { kind: "string" | "number" | "integer" | "boolean" | "null" }
  | { kind: "enum"; values: unknown[] }
  | { kind: "array"; items: TypeNode; minItems?: number; maxItems?: number }
  | { kind: "object"; properties: Record<string, TypeNode>; required?: string[] }
  | { kind: "ref"; targets: string[]; maxIncoming?: number };

export interface SchemaDef {
  name: string;
  version: number;
  root: TypeNode;
}

const PRIMITIVES = new Set(["string", "number", "integer", "boolean", "null"]);

const ALLOWED_KEYS: Record<string, Set<string>> = {
  string: new Set(["kind"]),
  number: new Set(["kind"]),
  integer: new Set(["kind"]),
  boolean: new Set(["kind"]),
  null: new Set(["kind"]),
  enum: new Set(["kind", "values"]),
  array: new Set(["kind", "items", "minItems", "maxItems"]),
  object: new Set(["kind", "properties", "required"]),
  ref: new Set(["kind", "targets", "maxIncoming"]),
};

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Validate that a rule tree stays inside the promised subset. Throws on violation. */
export function assertSubset(node: unknown, rulePath = ""): asserts node is TypeNode {
  if (!isPlainObject(node) || typeof node.kind !== "string" || !(node.kind in ALLOWED_KEYS)) {
    throw new Error(`unsupported-schema at ${rulePath || "/"}: missing or unknown "kind"`);
  }
  const kind = node.kind;
  for (const key of Object.keys(node)) {
    if (!ALLOWED_KEYS[kind].has(key)) {
      throw new Error(
        `unsupported-schema at ${rulePath || "/"}: keyword "${key}" is not in the promised subset for kind "${kind}"`,
      );
    }
  }
  switch (kind) {
    case "enum":
      if (!Array.isArray(node.values) || node.values.length === 0) {
        throw new Error(`unsupported-schema at ${rulePath}: "enum" requires a non-empty "values" array`);
      }
      break;
    case "array": {
      const n = node as { items?: unknown; minItems?: unknown; maxItems?: unknown };
      if (n.minItems !== undefined && (!Number.isInteger(n.minItems) || (n.minItems as number) < 0)) {
        throw new Error(`unsupported-schema at ${rulePath}: "minItems" must be a non-negative integer`);
      }
      if (n.maxItems !== undefined && (!Number.isInteger(n.maxItems) || (n.maxItems as number) < 0)) {
        throw new Error(`unsupported-schema at ${rulePath}: "maxItems" must be a non-negative integer`);
      }
      assertSubset(n.items, `${rulePath}/items`);
      break;
    }
    case "object": {
      const n = node as { properties?: unknown; required?: unknown };
      if (!isPlainObject(n.properties)) {
        throw new Error(`unsupported-schema at ${rulePath}: "object" requires a "properties" map`);
      }
      if (n.required !== undefined) {
        if (!Array.isArray(n.required) || n.required.some((r) => typeof r !== "string")) {
          throw new Error(`unsupported-schema at ${rulePath}: "required" must be an array of strings`);
        }
      }
      for (const [prop, child] of Object.entries(n.properties)) {
        assertSubset(child, `${rulePath}/properties/${escapePointer(prop)}`);
      }
      break;
    }
    case "ref": {
      const n = node as { targets?: unknown; maxIncoming?: unknown };
      if (!Array.isArray(n.targets) || n.targets.length === 0 || n.targets.some((t) => typeof t !== "string")) {
        throw new Error(`unsupported-schema at ${rulePath}: "ref" requires a non-empty "targets" string array`);
      }
      if (n.maxIncoming !== undefined && (!Number.isInteger(n.maxIncoming) || (n.maxIncoming as number) < 1)) {
        throw new Error(`unsupported-schema at ${rulePath}: "maxIncoming" must be an integer >= 1`);
      }
      break;
    }
  }
}

export function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** Versioned schema registry. Documents pin a type name and schema version. */
export class SchemaRegistry {
  private readonly defs = new Map<string, SchemaDef>();

  static key(name: string, version: number): string {
    return `${name}@${version}`;
  }

  register(def: SchemaDef): void {
    assertSubset(def.root);
    const key = SchemaRegistry.key(def.name, def.version);
    if (this.defs.has(key)) {
      throw new Error(`schema ${key} is already registered`);
    }
    this.defs.set(key, def);
  }

  /** Latest version when `version` is omitted. Returns undefined when absent. */
  get(name: string, version?: number): SchemaDef | undefined {
    if (version !== undefined) {
      return this.defs.get(SchemaRegistry.key(name, version));
    }
    let best: SchemaDef | undefined;
    for (const def of this.defs.values()) {
      if (def.name === name && (best === undefined || def.version > best.version)) {
        best = def;
      }
    }
    return best;
  }
}

/** One observed reference inside a document. */
export interface RefOccurrence {
  /** Referring document id. */
  source: string;
  /** Original JSON-pointer path of the ref value inside the source document. */
  instancePath: string;
  /** Path of the ref rule inside the schema rule tree. */
  rulePath: string;
  /** Schema key ("name@version") of the referring document. */
  schemaKey: string;
  /** Raw referenced id (the string value found in the data). */
  target: string;
  /** The ref rule node (for target-type and reverse-cardinality checks). */
  node: Extract<TypeNode, { kind: "ref" }>;
}

export interface WalkResult {
  errors: ValidationError[];
  refs: RefOccurrence[];
}

/**
 * Own validator (no external validation library): checks `data` against the
 * promised subset and collects every reference occurrence it walks past.
 * Descent into a subtree stops at the first kind mismatch, but sibling
 * subtrees keep being validated so one bad document yields all its errors.
 */
export function walkData(
  node: TypeNode,
  data: unknown,
  docId: string,
  schemaKey: string,
  instancePath: string,
  rulePath: string,
  out: WalkResult,
): void {
  const slice = [{ id: docId, path: instancePath, value: data }];
  switch (node.kind) {
    case "string":
    case "number":
    case "integer":
    case "boolean": {
      const ok =
        node.kind === "integer"
          ? typeof data === "number" && Number.isInteger(data)
          : typeof data === node.kind;
      if (!ok) {
        out.errors.push(
          makeError("type-mismatch", `expected ${node.kind}, got ${jsonTypeOf(data)}`, instancePath, rulePath, slice),
        );
      }
      return;
    }
    case "null":
      if (data !== null) {
        out.errors.push(makeError("type-mismatch", `expected null, got ${jsonTypeOf(data)}`, instancePath, rulePath, slice));
      }
      return;
    case "enum":
      if (!node.values.some((v) => deepEqual(v, data))) {
        out.errors.push(makeError("enum-mismatch", "value is not one of the allowed enum values", instancePath, rulePath, slice));
      }
      return;
    case "array": {
      if (!Array.isArray(data)) {
        out.errors.push(makeError("type-mismatch", `expected array, got ${jsonTypeOf(data)}`, instancePath, rulePath, slice));
        return;
      }
      if (node.minItems !== undefined && data.length < node.minItems) {
        out.errors.push(
          makeError("array-size", `expected at least ${node.minItems} items, got ${data.length}`, instancePath, rulePath, slice),
        );
      }
      if (node.maxItems !== undefined && data.length > node.maxItems) {
        out.errors.push(
          makeError("array-size", `expected at most ${node.maxItems} items, got ${data.length}`, instancePath, rulePath, slice),
        );
      }
      data.forEach((item, i) => {
        walkData(node.items, item, docId, schemaKey, `${instancePath}/${i}`, `${rulePath}/items`, out);
      });
      return;
    }
    case "object": {
      if (!isPlainObject(data)) {
        out.errors.push(makeError("type-mismatch", `expected object, got ${jsonTypeOf(data)}`, instancePath, rulePath, slice));
        return;
      }
      for (const req of node.required ?? []) {
        if (!(req in data)) {
          out.errors.push(
            makeError("missing-required", `missing required property "${req}"`, instancePath, `${rulePath}/required`, slice),
          );
        }
      }
      for (const [key, value] of Object.entries(data)) {
        const child = node.properties[key];
        const childInstance = `${instancePath}/${escapePointer(key)}`;
        if (child === undefined) {
          out.errors.push(
            makeError("unknown-property", `property "${key}" is not declared in the schema`, childInstance, rulePath, [
              { id: docId, path: childInstance, value },
            ]),
          );
          continue;
        }
        walkData(child, value, docId, schemaKey, childInstance, `${rulePath}/properties/${escapePointer(key)}`, out);
      }
      return;
    }
    case "ref": {
      if (typeof data !== "string") {
        out.errors.push(
          makeError("ref-not-string", `reference must be a string id, got ${jsonTypeOf(data)}`, instancePath, rulePath, slice),
        );
        return;
      }
      out.refs.push({ source: docId, instancePath, rulePath, schemaKey, target: data, node });
      return;
    }
  }
}

export function jsonTypeOf(v: unknown): string {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  return typeof v;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (typeof a !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((v, i) => deepEqual(v, (b as unknown[])[i]));
  }
  const ka = Object.keys(a as object);
  const kb = Object.keys(b as object);
  return (
    ka.length === kb.length &&
    ka.every((k) =>
      deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
    )
  );
}

/** Look up the rule node at a rule path (used to resolve maxIncoming per node). */
export function nodeAt(root: TypeNode, rulePath: string): TypeNode | undefined {
  const segs = rulePath.split("/").slice(1).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  let node: TypeNode | undefined = root;
  let i = 0;
  while (i < segs.length) {
    if (node === undefined) return undefined;
    const seg = segs[i];
    if (node.kind === "object" && seg === "properties") {
      node = node.properties[segs[i + 1]];
      i += 2;
    } else if (node.kind === "array" && seg === "items") {
      node = node.items;
      i += 1;
    } else {
      return undefined;
    }
  }
  return node;
}
