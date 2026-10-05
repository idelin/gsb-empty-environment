import {
  ArraySchema,
  JSONValue,
  ObjectSchema,
  OneOfSchema,
  PathSegment,
  Schema,
  SchemaDocument,
  ValidationError,
  ValidationOptions,
  ValidationResult,
} from "./schema";

/**
 * Errors are stored relative to the instance node being checked so that
 * results cached per (instance, schema) pair can be re-anchored at any
 * call site by prepending path segments.
 */
type RelErr = ValidationError;

interface Ctx {
  root: SchemaDocument;
  maxDepth: number;
  schemaIds: Map<object, number>;
  instanceIds: Map<object, number>;
  nextId: number;
  /** Completed verdicts keyed by (instance, schema, depth). */
  memo: Map<string, RelErr | null>;
  /** Pairs currently on the call stack; re-entering one is a cycle. */
  active: Set<string>;
}

export function validate(
  instance: JSONValue,
  doc: SchemaDocument,
  options: ValidationOptions = {}
): ValidationResult {
  const maxDepth = options.maxDepth === undefined ? Infinity : options.maxDepth;
  if (!(maxDepth >= 0)) {
    throw new RangeError("maxDepth must be a non-negative number");
  }
  const ctx: Ctx = {
    root: doc,
    maxDepth,
    schemaIds: new Map(),
    instanceIds: new Map(),
    nextId: 0,
    memo: new Map(),
    active: new Set(),
  };
  const err = check(ctx, instance, doc as Schema, 0);
  return err ? { valid: false, error: err } : { valid: true };
}

function check(
  ctx: Ctx,
  inst: JSONValue,
  schema: Schema,
  depth: number
): RelErr | null {
  const key = pairKey(ctx, inst, schema, depth);
  if (ctx.active.has(key)) {
    // Coinductive cycle break: a recursive schema revisited at the same
    // instance node and depth is accepted, so recursive schemas never
    // expand forever (e.g. an empty recursion like {"$ref": "#"}).
    return null;
  }
  const cached = ctx.memo.get(key);
  if (cached !== undefined) {
    return cached;
  }
  ctx.active.add(key);
  let result: RelErr | null;
  try {
    result = compute(ctx, inst, schema, depth);
  } finally {
    ctx.active.delete(key);
  }
  ctx.memo.set(key, result);
  return result;
}

function compute(
  ctx: Ctx,
  inst: JSONValue,
  schema: Schema,
  depth: number
): RelErr | null {
  const ref = (schema as { $ref?: string }).$ref;
  if (ref !== undefined) {
    const target = resolveRef(ctx.root, ref);
    if (target === null) {
      return { path: [], message: `unresolvable $ref "${ref}"` };
    }
    return check(ctx, inst, target, depth);
  }
  const oneOf = (schema as Partial<OneOfSchema>).oneOf;
  if (oneOf !== undefined) {
    return checkOneOf(ctx, inst, oneOf, depth);
  }
  const type = (schema as { type?: string }).type;
  switch (type) {
    case "null":
    case "boolean":
    case "number":
    case "string":
      return typeNameOf(inst) === type
        ? null
        : {
            path: [],
            message: `expected ${type}, got ${typeNameOf(inst)}`,
          };
    case "object":
      return checkObject(ctx, inst, schema as ObjectSchema, depth);
    case "array":
      return checkArray(ctx, inst, schema as ArraySchema, depth);
    default:
      return {
        path: [],
        message: `unsupported schema node ${JSON.stringify(schema)}`,
      };
  }
}

function checkObject(
  ctx: Ctx,
  inst: JSONValue,
  schema: ObjectSchema,
  depth: number
): RelErr | null {
  if (!isPlainObject(inst)) {
    return { path: [], message: `expected object, got ${typeNameOf(inst)}` };
  }
  let best: RelErr | null = null;
  for (const field of schema.required ?? []) {
    if (!Object.prototype.hasOwnProperty.call(inst, field)) {
      best = shallower(best, {
        path: [field],
        message: `missing required property "${field}"`,
      });
    }
  }
  const properties = schema.properties ?? {};
  for (const field of Object.keys(properties)) {
    if (!Object.prototype.hasOwnProperty.call(inst, field)) {
      continue;
    }
    const child = inst[field];
    const childDepth = containerDepth(ctx, child, depth);
    if (childDepth === null) {
      best = shallower(best, {
        path: [field],
        message: `instance depth budget exceeded (maxDepth ${ctx.maxDepth})`,
      });
      continue;
    }
    const sub = check(ctx, child, properties[field], childDepth);
    if (sub) {
      best = shallower(best, prepend(sub, field));
    }
  }
  return best;
}

function checkArray(
  ctx: Ctx,
  inst: JSONValue,
  schema: ArraySchema,
  depth: number
): RelErr | null {
  if (!Array.isArray(inst)) {
    return { path: [], message: `expected array, got ${typeNameOf(inst)}` };
  }
  let best: RelErr | null = null;
  if (schema.minItems !== undefined && inst.length < schema.minItems) {
    best = shallower(best, {
      path: [],
      message: `array length ${inst.length} is below minItems ${schema.minItems}`,
    });
  }
  if (schema.maxItems !== undefined && inst.length > schema.maxItems) {
    best = shallower(best, {
      path: [],
      message: `array length ${inst.length} exceeds maxItems ${schema.maxItems}`,
    });
  }
  if (schema.items !== undefined) {
    for (let i = 0; i < inst.length; i++) {
      const childDepth = containerDepth(ctx, inst[i], depth);
      if (childDepth === null) {
        best = shallower(best, {
          path: [i],
          message: `instance depth budget exceeded (maxDepth ${ctx.maxDepth})`,
        });
        continue;
      }
      const sub = check(ctx, inst[i], schema.items, childDepth);
      if (sub) {
        best = shallower(best, prepend(sub, i));
      }
    }
  }
  return best;
}

function checkOneOf(
  ctx: Ctx,
  inst: JSONValue,
  branches: Schema[],
  depth: number
): RelErr | null {
  const failures: { branch: number; error: RelErr }[] = [];
  const passed: number[] = [];
  for (let i = 0; i < branches.length; i++) {
    const err = check(ctx, inst, branches[i], depth);
    if (err) {
      failures.push({ branch: i, error: err });
    } else {
      passed.push(i);
    }
  }
  if (passed.length === 1) {
    return null;
  }
  if (passed.length === 0) {
    return {
      path: [],
      message: `matches none of the ${branches.length} mutually exclusive branches`,
      branches: failures,
    };
  }
  return {
    path: [],
    message: `matches ${passed.length} mutually exclusive branches (${passed.join(
      ", "
    )}); exactly one required`,
  };
}

function resolveRef(root: SchemaDocument, ref: string): Schema | null {
  if (ref === "#") {
    return root as Schema;
  }
  const m = /^#\/definitions\/([^/]+)$/.exec(ref);
  if (
    m &&
    root.definitions !== undefined &&
    Object.prototype.hasOwnProperty.call(root.definitions, m[1])
  ) {
    return root.definitions[m[1]];
  }
  return null;
}

function pairKey(
  ctx: Ctx,
  inst: JSONValue,
  schema: Schema,
  depth: number
): string {
  return `${instanceKey(ctx, inst)}|${schemaId(ctx, schema)}|${depth}`;
}

function instanceKey(ctx: Ctx, inst: JSONValue): string {
  if (inst !== null && typeof inst === "object") {
    return `o${idOf(ctx.instanceIds, ctx, inst)}`;
  }
  return `p${typeof inst}:${String(inst)}`;
}

function schemaId(ctx: Ctx, schema: Schema): number {
  return idOf(ctx.schemaIds, ctx, schema as object);
}

function idOf(map: Map<object, number>, ctx: Ctx, node: object): number {
  let id = map.get(node);
  if (id === undefined) {
    id = ctx.nextId++;
    map.set(node, id);
  }
  return id;
}

function isPlainObject(
  value: JSONValue
): value is { [key: string]: JSONValue } {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * Depth of a child node: containers nest one level deeper than their parent,
 * scalar leaves inherit the parent depth. Returns null when the child would
 * exceed the depth budget.
 */
function containerDepth(
  ctx: Ctx,
  child: JSONValue,
  parentDepth: number
): number | null {
  const isContainer = child !== null && typeof child === "object";
  const depth = isContainer ? parentDepth + 1 : parentDepth;
  return depth > ctx.maxDepth ? null : depth;
}

function typeNameOf(value: JSONValue): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

/** Re-anchor a relative error one segment up, including nested branches. */
function prepend(err: RelErr, segment: PathSegment): RelErr {
  const out: RelErr = {
    path: [segment, ...err.path],
    message: err.message,
  };
  if (err.branches) {
    out.branches = err.branches.map((b) => ({
      branch: b.branch,
      error: prepend(b.error, segment),
    }));
  }
  return out;
}

/**
 * Keep the shallower error; on equal depth, the lexicographically smaller
 * path wins (numeric indices sort before string keys).
 */
function shallower(a: RelErr | null, b: RelErr | null): RelErr | null {
  if (a === null) return b;
  if (b === null) return a;
  return comparePaths(a.path, b.path) <= 0 ? a : b;
}

function comparePaths(a: PathSegment[], b: PathSegment[]): number {
  if (a.length !== b.length) {
    return a.length - b.length;
  }
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (x === y) continue;
    if (typeof x === "number" && typeof y === "number") return x - y;
    if (typeof x === "number") return -1;
    if (typeof y === "number") return 1;
    return x < y ? -1 : 1;
  }
  return 0;
}

/** Renders a path like $.items[2].name */
export function formatPath(path: PathSegment[]): string {
  let out = "$";
  for (const segment of path) {
    if (typeof segment === "number") {
      out += `[${segment}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(segment)) {
      out += `.${segment}`;
    } else {
      out += `[${JSON.stringify(segment)}]`;
    }
  }
  return out;
}

/** Human-readable rendering of an error, expanding oneOf branch failures. */
export function explainError(err: ValidationError, indent = ""): string {
  let out = `${indent}${formatPath(err.path)}: ${err.message}`;
  for (const failure of err.branches ?? []) {
    out +=
      `\n${indent}  branch ${failure.branch} failed:\n` +
      explainError(failure.error, indent + "    ");
  }
  return out;
}
