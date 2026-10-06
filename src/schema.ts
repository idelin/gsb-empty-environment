/**
 * 模式（Schema）定义与记录校验。
 *
 * 类型语言（全部为可 JSON 序列化的判别联合）：
 * - string / number / boolean：原始类型
 * - enum：有限枚举域，值为 string | number
 * - nullable：值级可空（允许 null），与字段级 required（是否必须出现）正交
 * - object：具名字段集合，字段带 required 标记
 * - array：元素模式 + 长度界 [minLen, maxLen]
 */

export type EnumValue = string | number;

export type Schema =
  | { readonly kind: "string" }
  | { readonly kind: "number" }
  | { readonly kind: "boolean" }
  | { readonly kind: "enum"; readonly values: readonly EnumValue[] }
  | { readonly kind: "nullable"; readonly inner: Schema }
  | { readonly kind: "object"; readonly fields: Readonly<Record<string, Field>> }
  | {
      readonly kind: "array";
      readonly items: Schema;
      readonly minLen?: number;
      readonly maxLen?: number;
    };

export interface Field {
  readonly schema: Schema;
  /** true = 记录必须携带该字段；false = 可缺省 */
  readonly required: boolean;
}

/** 构造器，让模式字面量更易写 */
export const t = {
  string: (): Schema => ({ kind: "string" }),
  number: (): Schema => ({ kind: "number" }),
  boolean: (): Schema => ({ kind: "boolean" }),
  enum: (values: readonly EnumValue[]): Schema => ({ kind: "enum", values }),
  nullable: (inner: Schema): Schema => ({ kind: "nullable", inner }),
  object: (fields: Record<string, Field>): Schema => ({ kind: "object", fields }),
  array: (items: Schema, opts?: { minLen?: number; maxLen?: number }): Schema => ({
    kind: "array",
    items,
    ...opts,
  }),
  req: (schema: Schema): Field => ({ schema, required: true }),
  opt: (schema: Schema): Field => ({ schema, required: false }),
};

export function arrayMinLen(s: Extract<Schema, { kind: "array" }>): number {
  return s.minLen ?? 0;
}

export function arrayMaxLen(s: Extract<Schema, { kind: "array" }>): number {
  return s.maxLen ?? Number.POSITIVE_INFINITY;
}

export interface ValidateOptions {
  /** false 时对象为封闭：记录不得携带模式未声明的字段。默认 true（开放） */
  readonly allowExtraFields?: boolean;
}

/** 校验一个具体值是否被模式接受 */
export function validate(
  schema: Schema,
  value: unknown,
  options: ValidateOptions = {},
): boolean {
  switch (schema.kind) {
    case "string":
      return typeof value === "string";
    case "number":
      return typeof value === "number" && Number.isFinite(value);
    case "boolean":
      return typeof value === "boolean";
    case "enum":
      return schema.values.some((v) => v === value);
    case "nullable":
      return value === null || validate(schema.inner, value, options);
    case "object": {
      if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
      const rec = value as Record<string, unknown>;
      if (options.allowExtraFields === false) {
        for (const key of Object.keys(rec)) {
          if (!Object.prototype.hasOwnProperty.call(schema.fields, key)) return false;
        }
      }
      for (const [name, field] of Object.entries(schema.fields)) {
        const present = Object.prototype.hasOwnProperty.call(rec, name);
        if (!present) {
          if (field.required) return false;
          continue;
        }
        if (!validate(field.schema, rec[name], options)) return false;
      }
      return true;
    }
    case "array": {
      if (!Array.isArray(value)) return false;
      if (value.length < arrayMinLen(schema) || value.length > arrayMaxLen(schema)) return false;
      return value.every((item) => validate(schema.items, item, options));
    }
  }
}

/**
 * 构造模式下的最小具体值（用于组装反例记录）。
 * 约定：string→""，number→0，boolean→false，enum→首个声明值，
 * nullable→null，object→仅含必填字段，array→minLen 个最小元素。
 */
export function minValue(schema: Schema): unknown {
  switch (schema.kind) {
    case "string":
      return "";
    case "number":
      return 0;
    case "boolean":
      return false;
    case "enum":
      return schema.values[0];
    case "nullable":
      return null;
    case "object": {
      const rec: Record<string, unknown> = {};
      for (const [name, field] of Object.entries(schema.fields)) {
        if (field.required) rec[name] = minValue(field.schema);
      }
      return rec;
    }
    case "array": {
      const n = arrayMinLen(schema);
      const item = minValue(schema.items);
      return Array.from({ length: n }, () => item);
    }
  }
}

/** 值的规模度量：节点数。用于在多个候选反例中挑选最小者。 */
export function valueSize(value: unknown): number {
  if (Array.isArray(value)) {
    return 1 + value.reduce((acc, v) => acc + valueSize(v), 0);
  }
  if (typeof value === "object" && value !== null) {
    return 1 + Object.values(value).reduce((acc: number, v) => acc + valueSize(v), 0);
  }
  return 1;
}
