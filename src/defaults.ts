import { ExprEvalError, evalExpr, exprDependencies } from "./expr.ts";
import {
  deepClone,
  isPlainObject,
  joinPointer,
  type DefaultSpec,
  type Issue,
  type JsonObject,
  type JsonValue,
  type Schema,
} from "./schema.ts";

function isExprDefault(d: DefaultSpec): d is { $expr: import("./schema.ts").Expr } {
  return isPlainObject(d) && isPlainObject(d.$expr);
}

/**
 * Recursively fill defaults for missing object properties.
 * Mutates `value` (expected to be an owned clone). Appends issues on
 * dependency cycles or expression evaluation failures.
 */
export function fillDefaults(
  schema: Schema,
  value: JsonValue,
  instancePath: string,
  schemaPath: string,
  issues: Issue[],
): void {
  if (schema.type === "object" && isPlainObject(value)) {
    applyObjectDefaults(schema, value, instancePath, schemaPath, issues);
    const props = schema.properties ?? {};
    for (const [key, propSchema] of Object.entries(props)) {
      if (Object.hasOwn(value, key)) {
        fillDefaults(propSchema, value[key], joinPointer(instancePath, key), `${schemaPath}/properties/${escape(key)}`, issues);
      }
    }
  } else if (schema.type === "array" && Array.isArray(value) && schema.items) {
    value.forEach((item, i) => {
      fillDefaults(schema.items as Schema, item, `${instancePath}/${i}`, `${schemaPath}/items`, issues);
    });
  }
}

function escape(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

function applyObjectDefaults(
  schema: Schema,
  obj: JsonObject,
  instancePath: string,
  schemaPath: string,
  issues: Issue[],
): void {
  const props = schema.properties ?? {};
  const pending = new Map<string, DefaultSpec>();
  for (const [key, propSchema] of Object.entries(props)) {
    if (!Object.hasOwn(obj, key) && propSchema.default !== undefined) {
      pending.set(key, propSchema.default);
    }
  }
  if (pending.size === 0) return;

  // Dependency edges only constrain ordering among pending defaults;
  // references to already-present fields are resolved directly.
  const deps = new Map<string, Set<string>>();
  for (const [key, spec] of pending) {
    const needed = isExprDefault(spec) ? exprDependencies(spec.$expr) : new Set<string>();
    deps.set(key, new Set([...needed].filter((d) => pending.has(d))));
  }

  const order = topoOrder(deps);
  if (!order.ok) {
    issues.push({
      instancePath,
      schemaPath: `${schemaPath}/properties`,
      keyword: "defaultCycle",
      message: `default dependency cycle: ${[...order.cycle, order.cycle[0]].join(" -> ")}`,
    });
    return;
  }

  for (const key of order.sorted) {
    const spec = pending.get(key) as DefaultSpec;
    const propSchemaPath = `${schemaPath}/properties/${escape(key)}/default`;
    try {
      // Deep-clone every materialized default so no two instance locations
      // (e.g. distinct array elements) ever share a mutable reference.
      obj[key] = isExprDefault(spec) ? deepClone(evalExpr(spec.$expr, obj)) : deepClone(spec);
    } catch (err) {
      if (err instanceof ExprEvalError) {
        issues.push({
          instancePath: joinPointer(instancePath, key),
          schemaPath: propSchemaPath,
          keyword: "default",
          message: `default evaluation failed: ${err.message}`,
        });
      } else {
        throw err;
      }
    }
  }
}

type TopoResult = { ok: true; sorted: string[] } | { ok: false; cycle: string[] };

function topoOrder(deps: Map<string, Set<string>>): TopoResult {
  const WHITE = 0, GRAY = 1, BLACK = 2;
  const color = new Map<string, number>([...deps.keys()].map((k) => [k, WHITE]));
  const sorted: string[] = [];
  const stack: string[] = [];

  const visit = (node: string): string[] | null => {
    color.set(node, GRAY);
    stack.push(node);
    for (const dep of deps.get(node) ?? []) {
      const c = color.get(dep);
      if (c === GRAY) {
        return stack.slice(stack.indexOf(dep));
      }
      if (c === WHITE) {
        const cycle = visit(dep);
        if (cycle) return cycle;
      }
    }
    stack.pop();
    color.set(node, BLACK);
    sorted.push(node);
    return null;
  };

  for (const key of deps.keys()) {
    if (color.get(key) === WHITE) {
      const cycle = visit(key);
      if (cycle) return { ok: false, cycle };
    }
  }
  return { ok: true, sorted };
}
