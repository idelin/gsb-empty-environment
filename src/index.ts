import type { JsonValue, Result, Schema } from "./schema.ts";
import { DefaultResolver } from "./defaults.ts";
import { validateSchema } from "./validator.ts";

export type { JsonValue, JsonObject, Schema, ValidationError, Result } from "./schema.ts";
export { SchemaError } from "./validator.ts";

/**
 * Validates `input` against the supported schema subset and fills defaults
 * for missing fields.
 *
 * Atomicity: the input is deep-cloned up front and never mutated. Defaults
 * are applied on the clone, then the filled document is validated. If any
 * error (validation, unresolvable or circular default) occurs, the whole
 * fill is rolled back: the result carries only errors, no partial data.
 */
export function validateAndFill(schema: Schema, input: JsonValue): Result {
  let data: JsonValue;
  try {
    data = structuredClone(input);
  } catch {
    throw new TypeError("input must be structured-cloneable JSON data");
  }

  const resolver = new DefaultResolver(schema, data);
  resolver.fillAll();

  const errors = [...resolver.errors];
  validateSchema(schema, data, "", "#", errors);

  if (errors.length > 0) return { ok: false, errors };
  return { ok: true, data };
}
