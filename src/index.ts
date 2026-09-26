import { fillDefaults } from "./defaults.ts";
import { validate } from "./validate.ts";
import { deepClone, type JsonValue, type Result, type Schema } from "./schema.ts";

export * from "./schema.ts";
export { evalExpr, exprDependencies } from "./expr.ts";

/**
 * Fill defaults for missing fields, then validate. All-or-nothing:
 * the input is never mutated; on any failure the original instance
 * is effectively rolled back and only the collected issues are returned.
 */
export function applyDefaultsAndValidate(schema: Schema, instance: JsonValue): Result {
  const working = deepClone(instance);
  const issues: import("./schema.ts").Issue[] = [];

  fillDefaults(schema, working, "", "#", issues);
  if (issues.length > 0) return { ok: false, errors: issues };

  validate(schema, working, "", "#", issues);
  if (issues.length > 0) return { ok: false, errors: issues };

  return { ok: true, value: working };
}
