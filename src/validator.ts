/**
 * Hand-written JSON Schema validator for a deliberately small, explicitly
 * declared subset. No third-party validator is used.
 *
 * Only the keywords listed in SUPPORTED_KEYWORDS are enforced. Any other
 * keyword is ignored and reported as a warning, so callers never mistake
 * unimplemented rules for validated ones.
 */

export const SUPPORTED_KEYWORDS = [
  "type",
  "enum",
  "const",
  "properties",
  "required",
  "additionalProperties",
  "items",
  "minItems",
  "maxItems",
  "minLength",
  "maxLength",
  "pattern",
  "minimum",
  "maximum",
] as const;

export type SupportedKeyword = (typeof SUPPORTED_KEYWORDS)[number];

export type SchemaNode = boolean | { [keyword: string]: unknown };

export interface ValidationError {
  /** JSON Pointer into the instance, "" means the root value. */
  instancePath: string;
  /** JSON Pointer into the schema, identifying the rule that failed. */
  rulePath: string;
  keyword: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
  /** Unsupported keywords encountered while walking the schema. */
  warnings: string[];
}

const SUPPORTED = new Set<string>(SUPPORTED_KEYWORDS);
const META_KEYWORDS = new Set(["$schema", "$id", "title", "description"]);

function escapePointerSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

export function joinPointer(base: string, segment: string): string {
  return base + "/" + escapePointerSegment(segment);
}

function typeOf(value: unknown): string {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  return typeof value;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  if (typeof a === "object") {
    const aObj = a as Record<string, unknown>;
    const bObj = b as Record<string, unknown>;
    const aKeys = Object.keys(aObj);
    const bKeys = Object.keys(bObj);
    if (aKeys.length !== bKeys.length) return false;
    return aKeys.every((k) => k in bObj && deepEqual(aObj[k], bObj[k]));
  }
  return false;
}

function matchesType(value: unknown, expected: string): boolean {
  if (expected === "integer") return typeof value === "number" && Number.isInteger(value);
  if (expected === "number") return typeof value === "number";
  return typeOf(value) === expected;
}

interface Ctx {
  errors: ValidationError[];
  warnings: string[];
  warned: Set<string>;
}

/** Static scan: unsupported keywords are reported even for branches no sample reaches. */
function scanSchema(schema: SchemaNode, rulePath: string, ctx: Ctx): void {
  if (schema === true || schema === false) return;
  if (typeof schema !== "object" || schema === null || Array.isArray(schema)) return;

  for (const key of Object.keys(schema)) {
    if (!SUPPORTED.has(key) && !META_KEYWORDS.has(key) && !ctx.warned.has(rulePath + "#" + key)) {
      ctx.warned.add(rulePath + "#" + key);
      ctx.warnings.push(`unsupported keyword "${key}" at ${rulePath || "/"} ignored`);
    }
  }

  const properties = (schema.properties ?? {}) as Record<string, SchemaNode>;
  for (const [name, sub] of Object.entries(properties)) {
    scanSchema(sub, joinPointer(rulePath, "properties") + "/" + escapePointerSegment(name), ctx);
  }
  if (schema.items !== undefined) scanSchema(schema.items as SchemaNode, joinPointer(rulePath, "items"), ctx);
  if (typeof schema.additionalProperties === "object" && schema.additionalProperties !== null) {
    scanSchema(schema.additionalProperties as SchemaNode, joinPointer(rulePath, "additionalProperties"), ctx);
  }
}

function fail(ctx: Ctx, instancePath: string, rulePath: string, keyword: string, message: string): void {
  ctx.errors.push({ instancePath, rulePath, keyword, message });
}

function validateNode(instance: unknown, schema: SchemaNode, instancePath: string, rulePath: string, ctx: Ctx): void {
  if (schema === true) return;
  if (schema === false) {
    fail(ctx, instancePath, rulePath, "false", "value is not allowed here");
    return;
  }
  if (typeof schema !== "object" || schema === null || Array.isArray(schema)) {
    ctx.warnings.push(`schema at ${rulePath || "/"} is not a valid schema object; ignored`);
    return;
  }

  for (const key of Object.keys(schema)) {
    if (!SUPPORTED.has(key) && !META_KEYWORDS.has(key) && !ctx.warned.has(rulePath + "#" + key)) {
      ctx.warned.add(rulePath + "#" + key);
      ctx.warnings.push(`unsupported keyword "${key}" at ${rulePath || "/"} ignored`);
    }
  }

  if ("type" in schema) {
    const expected = schema.type;
    const list = Array.isArray(expected) ? expected : [expected];
    if (!list.some((t) => typeof t === "string" && matchesType(instance, t))) {
      fail(ctx, instancePath, joinPointer(rulePath, "type"), "type",
        `expected type ${list.join("|")}, got ${typeOf(instance)}`);
    }
  }

  if ("const" in schema && !deepEqual(instance, schema.const)) {
    fail(ctx, instancePath, joinPointer(rulePath, "const"), "const",
      `expected constant ${JSON.stringify(schema.const)}`);
  }

  if ("enum" in schema && Array.isArray(schema.enum)) {
    if (!schema.enum.some((option) => deepEqual(instance, option))) {
      fail(ctx, instancePath, joinPointer(rulePath, "enum"), "enum",
        `value not in enum ${JSON.stringify(schema.enum)}`);
    }
  }

  if (typeof instance === "number") {
    if (typeof schema.minimum === "number" && instance < schema.minimum) {
      fail(ctx, instancePath, joinPointer(rulePath, "minimum"), "minimum",
        `${instance} < minimum ${schema.minimum}`);
    }
    if (typeof schema.maximum === "number" && instance > schema.maximum) {
      fail(ctx, instancePath, joinPointer(rulePath, "maximum"), "maximum",
        `${instance} > maximum ${schema.maximum}`);
    }
  }

  if (typeof instance === "string") {
    if (typeof schema.minLength === "number" && instance.length < schema.minLength) {
      fail(ctx, instancePath, joinPointer(rulePath, "minLength"), "minLength",
        `length ${instance.length} < minLength ${schema.minLength}`);
    }
    if (typeof schema.maxLength === "number" && instance.length > schema.maxLength) {
      fail(ctx, instancePath, joinPointer(rulePath, "maxLength"), "maxLength",
        `length ${instance.length} > maxLength ${schema.maxLength}`);
    }
    if (typeof schema.pattern === "string") {
      let re: RegExp | null = null;
      try {
        re = new RegExp(schema.pattern);
      } catch {
        ctx.warnings.push(`invalid pattern at ${joinPointer(rulePath, "pattern")} ignored`);
      }
      if (re && !re.test(instance)) {
        fail(ctx, instancePath, joinPointer(rulePath, "pattern"), "pattern",
          `does not match pattern ${schema.pattern}`);
      }
    }
  }

  if (Array.isArray(instance)) {
    if (typeof schema.minItems === "number" && instance.length < schema.minItems) {
      fail(ctx, instancePath, joinPointer(rulePath, "minItems"), "minItems",
        `${instance.length} items < minItems ${schema.minItems}`);
    }
    if (typeof schema.maxItems === "number" && instance.length > schema.maxItems) {
      fail(ctx, instancePath, joinPointer(rulePath, "maxItems"), "maxItems",
        `${instance.length} items > maxItems ${schema.maxItems}`);
    }
    if (schema.items !== undefined) {
      const itemRule = joinPointer(rulePath, "items");
      instance.forEach((item, i) => {
        validateNode(item, schema.items as SchemaNode, joinPointer(instancePath, String(i)), itemRule, ctx);
      });
    }
  }

  if (instance !== null && typeof instance === "object" && !Array.isArray(instance)) {
    const obj = instance as Record<string, unknown>;

    if (Array.isArray(schema.required)) {
      for (const name of schema.required) {
        if (typeof name === "string" && !(name in obj)) {
          fail(ctx, instancePath, joinPointer(rulePath, "required"), "required",
            `missing required property "${name}"`);
        }
      }
    }

    const properties = (schema.properties ?? {}) as Record<string, SchemaNode>;
    if (schema.properties !== undefined && (typeof schema.properties !== "object" || schema.properties === null)) {
      ctx.warnings.push(`"properties" at ${rulePath || "/"} is not an object; ignored`);
    }
    for (const [name, subSchema] of Object.entries(properties)) {
      if (name in obj) {
        validateNode(obj[name], subSchema, joinPointer(instancePath, name), joinPointer(rulePath, "properties") + "/" + escapePointerSegment(name), ctx);
      }
    }

    if (schema.additionalProperties === false) {
      for (const name of Object.keys(obj)) {
        if (!(name in properties)) {
          fail(ctx, joinPointer(instancePath, name), joinPointer(rulePath, "additionalProperties"), "additionalProperties",
            `unexpected property "${name}"`);
        }
      }
    } else if (typeof schema.additionalProperties === "object" && schema.additionalProperties !== null) {
      const apRule = joinPointer(rulePath, "additionalProperties");
      for (const name of Object.keys(obj)) {
        if (!(name in properties)) {
          validateNode(obj[name], schema.additionalProperties as SchemaNode, joinPointer(instancePath, name), apRule, ctx);
        }
      }
    }
  }
}

export function validate(instance: unknown, schema: SchemaNode): ValidationResult {
  const ctx: Ctx = { errors: [], warnings: [], warned: new Set() };
  scanSchema(schema, "", ctx);
  validateNode(instance, schema, "", "", ctx);
  return { valid: ctx.errors.length === 0, errors: ctx.errors, warnings: ctx.warnings };
}
