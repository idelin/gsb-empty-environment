import type { Schema, SchemaType } from "./schema.ts";

/** 单条验证错误：同时给出实例路径与规则（schema）路径。 */
export interface ValidationError {
  /** 指向文档中出错位置的 JSON Pointer，如 /address/zip */
  instancePath: string;
  /** 指向 schema 中触发规则的 JSON Pointer，如 /properties/address/properties/zip/pattern */
  schemaPath: string;
  keyword: string;
  message: string;
}

function escapePointer(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function joinPath(base: string, segment: string): string {
  return base + "/" + escapePointer(segment);
}

function typeOf(value: unknown): SchemaType {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  if (typeof value === "number") return "number";
  return typeof value as SchemaType;
}

function matchesType(value: unknown, expected: SchemaType): boolean {
  if (expected === "integer") {
    return typeof value === "number" && Number.isInteger(value);
  }
  if (expected === "number") {
    return typeof value === "number";
  }
  return typeOf(value) === expected;
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((item, i) => deepEqual(item, b[i]))
    );
  }
  const ra = a as Record<string, unknown>;
  const rb = b as Record<string, unknown>;
  const ka = Object.keys(ra);
  const kb = Object.keys(rb);
  return (
    ka.length === kb.length && ka.every((k) => k in rb && deepEqual(ra[k], rb[k]))
  );
}

/**
 * 手写验证器：不依赖任何现成验证库。
 * 只兑现 schema.ts 中声明的关键学子集，逐条收集错误（非遇错即停）。
 */
export function validate(instance: unknown, schema: Schema): ValidationError[] {
  const errors: ValidationError[] = [];
  walk(instance, schema, "", "", errors);
  return errors;
}

function walk(
  instance: unknown,
  schema: Schema,
  instancePath: string,
  schemaPath: string,
  errors: ValidationError[],
): void {
  const fail = (keyword: string, message: string, keywordPath: string): void => {
    errors.push({ instancePath, schemaPath: keywordPath, keyword, message });
  };

  if (schema.type !== undefined) {
    const allowed = Array.isArray(schema.type) ? schema.type : [schema.type];
    if (!allowed.some((t) => matchesType(instance, t))) {
      fail(
        "type",
        `expected type ${allowed.join(" | ")}, got ${typeOf(instance)}`,
        joinPath(schemaPath, "type"),
      );
      return; // 类型不符时继续深入没有意义
    }
  }

  if (schema.const !== undefined && !deepEqual(instance, schema.const)) {
    fail("const", `must equal ${JSON.stringify(schema.const)}`, joinPath(schemaPath, "const"));
  }

  if (schema.enum !== undefined && !schema.enum.some((v) => deepEqual(v, instance))) {
    fail("enum", `must be one of ${JSON.stringify(schema.enum)}`, joinPath(schemaPath, "enum"));
  }

  if (typeof instance === "number") {
    if (schema.minimum !== undefined && instance < schema.minimum) {
      fail("minimum", `must be >= ${schema.minimum}`, joinPath(schemaPath, "minimum"));
    }
    if (schema.maximum !== undefined && instance > schema.maximum) {
      fail("maximum", `must be <= ${schema.maximum}`, joinPath(schemaPath, "maximum"));
    }
    if (schema.exclusiveMinimum !== undefined && instance <= schema.exclusiveMinimum) {
      fail("exclusiveMinimum", `must be > ${schema.exclusiveMinimum}`, joinPath(schemaPath, "exclusiveMinimum"));
    }
    if (schema.exclusiveMaximum !== undefined && instance >= schema.exclusiveMaximum) {
      fail("exclusiveMaximum", `must be < ${schema.exclusiveMaximum}`, joinPath(schemaPath, "exclusiveMaximum"));
    }
  }

  if (typeof instance === "string") {
    if (schema.minLength !== undefined && instance.length < schema.minLength) {
      fail("minLength", `length must be >= ${schema.minLength}`, joinPath(schemaPath, "minLength"));
    }
    if (schema.maxLength !== undefined && instance.length > schema.maxLength) {
      fail("maxLength", `length must be <= ${schema.maxLength}`, joinPath(schemaPath, "maxLength"));
    }
    if (schema.pattern !== undefined && !new RegExp(schema.pattern).test(instance)) {
      fail("pattern", `must match /${schema.pattern}/`, joinPath(schemaPath, "pattern"));
    }
  }

  if (Array.isArray(instance)) {
    if (schema.minItems !== undefined && instance.length < schema.minItems) {
      fail("minItems", `must have >= ${schema.minItems} items`, joinPath(schemaPath, "minItems"));
    }
    if (schema.maxItems !== undefined && instance.length > schema.maxItems) {
      fail("maxItems", `must have <= ${schema.maxItems} items`, joinPath(schemaPath, "maxItems"));
    }
    if (schema.items !== undefined) {
      const itemsPath = joinPath(schemaPath, "items");
      instance.forEach((item, i) => {
        walk(item, schema.items as Schema, joinPath(instancePath, String(i)), itemsPath, errors);
      });
    }
  }

  if (typeOf(instance) === "object") {
    const record = instance as Record<string, unknown>;
    if (schema.required !== undefined) {
      for (const key of schema.required) {
        if (!(key in record)) {
          fail("required", `missing required property "${key}"`, joinPath(schemaPath, "required"));
        }
      }
    }
    if (schema.properties !== undefined) {
      for (const [key, subSchema] of Object.entries(schema.properties)) {
        if (key in record) {
          walk(
            record[key],
            subSchema,
            joinPath(instancePath, key),
            joinPath(joinPath(schemaPath, "properties"), key),
            errors,
          );
        }
      }
    }
    if (schema.additionalProperties !== undefined) {
      const known = new Set(Object.keys(schema.properties ?? {}));
      for (const key of Object.keys(record)) {
        if (known.has(key)) continue;
        if (schema.additionalProperties === false) {
          fail(
            "additionalProperties",
            `unexpected property "${key}"`,
            joinPath(schemaPath, "additionalProperties"),
          );
        } else if (typeof schema.additionalProperties === "object") {
          walk(
            record[key],
            schema.additionalProperties,
            joinPath(instancePath, key),
            joinPath(schemaPath, "additionalProperties"),
            errors,
          );
        }
      }
    }
  }
}
