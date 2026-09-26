/**
 * 子集校验器（手写实现，无第三方 JSON Schema 验证库）。
 *
 * 每个校验错误同时给出：
 * - instancePath：实例数据上的 JSON Pointer（# 表示根；字符串内问题附
 *   原文字符串的 UTF-16 范围 range）；
 * - rulePath：对应规则在 schema 中的 JSON Pointer。
 *
 * 校验从不修改实例；数组里越界的元素即使整体被拒绝，也会为每个越界项
 * 单独报错，绝不静默丢弃。
 */

import { NormalizedText, type TextRange } from "./unicode.ts";
import {
  compileSchema,
  escapePointerToken,
  isConstSet,
  type CompiledSchema,
  type InstanceType,
  type Json,
  type SchemaError,
} from "./schema.ts";

export interface ValidationError {
  /** 实例路径，JSON Pointer 风格，根为 "#"。 */
  instancePath: string;
  /** 规则路径（schema 内 JSON Pointer），根为 "#"。 */
  rulePath: string;
  keyword: string;
  message: string;
  /** 字符串值内部问题时，原文（未规范化）上的 UTF-16 区间。 */
  range?: TextRange;
  /** 与 range 对应的原文片段，便于核对组合/规范化位置。 */
  excerpt?: string;
}

function instanceTypeName(value: Json): InstanceType {
  if (value === null) {
    return "null";
  }
  if (Array.isArray(value)) {
    return "array";
  }
  if (typeof value === "number") {
    return Number.isInteger(value) ? "integer" : "number";
  }
  return typeof value as "boolean" | "string" | "object";
}

function matchesType(value: Json, allowed: InstanceType[]): boolean {
  const actual = instanceTypeName(value);
  if (allowed.includes(actual)) {
    return true;
  }
  // integer 也是 number
  return actual === "integer" && allowed.includes("number");
}

function deepEqual(left: Json, right: Json): boolean {
  if (left === right) {
    return true;
  }
  if (typeof left !== typeof right || typeof left !== "object") {
    return false;
  }
  if (left === null || right === null) {
    return left === right;
  }
  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) {
      return false;
    }
    return left.every((entry, index) => deepEqual(entry, right[index]));
  }
  const leftObj = left as Record<string, Json>;
  const rightObj = right as Record<string, Json>;
  const leftKeys = Object.keys(leftObj);
  const rightKeys = Object.keys(rightObj);
  return (
    leftKeys.length === rightKeys.length &&
    leftKeys.every((key) => Object.prototype.hasOwnProperty.call(rightObj, key) && deepEqual(leftObj[key], rightObj[key]))
  );
}

function childInstancePath(base: string, key: string): string {
  return `${base}/${escapePointerToken(key)}`;
}

function excerptFor(original: string, range: TextRange): string {
  const slice = original.slice(range.start, range.end);
  return Array.from(slice)
    .map((char) => {
      const cp = char.codePointAt(0) ?? 0;
      if (cp < 0x20 || (cp >= 0x7f && cp < 0xa0) || cp === 0x200e || cp === 0x200f || (cp >= 0x202a && cp <= 0x2069)) {
        return `U+${cp.toString(16).toUpperCase().padStart(4, "0")}`;
      }
      return char;
    })
    .join("");
}

function validateString(
  value: string,
  schema: CompiledSchema,
  instancePath: string,
  rulePath: string,
  errors: ValidationError[],
): void {
  const options = schema.stringOptions!;
  const text = new NormalizedText(value, options.normalization);

  if (options.bidiControl === "deny") {
    for (const run of text.bidiControlsInOriginal()) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/bidiControl`,
        keyword: "bidiControl",
        message: `包含被拒绝的双向格式控制字符 U+${run.codePoint.toString(16).toUpperCase().padStart(4, "0")}（策略：deny）；该字符不会被静默删除`,
        range: run.range,
        excerpt: excerptFor(value, run.range),
      });
    }
  }

  if (options.scriptMatchers.length > 0) {
    for (const run of text.runs) {
      const allowed = options.scriptMatchers.some((matcher) => matcher.test(run.char));
      if (!allowed) {
        errors.push({
          instancePath,
          rulePath: `${rulePath}/allowedScripts`,
          keyword: "allowedScripts",
          message: `码点 U+${run.codePoint.toString(16).toUpperCase().padStart(4, "0")} 不属于允许的脚本集合 [${options.allowedScripts.join(", ")}]`,
          range: run.range,
          excerpt: excerptFor(value, run.range),
        });
      }
    }
  }

  if (options.minLength !== null) {
    const actual = text.length(options.minLengthUnit);
    if (actual < options.minLength) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/minLength`,
        keyword: "minLength",
        message: `长度 ${actual} ${options.minLengthUnit} 小于下限 ${options.minLength}（规范化：${options.normalization}）`,
      });
    }
  }

  if (options.maxLength !== null) {
    const unit = options.maxLengthUnit;
    const actual = text.length(unit);
    if (actual > options.maxLength) {
      let range: TextRange | undefined;
      if (value.length > 0) {
        if (unit === "graphemes") {
          const clusters = text.graphemeRuns();
          const overflow = clusters.slice(options.maxLength);
          if (overflow.length > 0) {
            range = { start: overflow[0].start, end: overflow[overflow.length - 1].end };
          }
        } else {
          const normalizedRange =
            unit === "utf16"
              ? { start: options.maxLength, end: text.normalized.length }
              : codepointRange(text.normalized, options.maxLength);
          range = text.mapNormalizedRangeToOriginal(normalizedRange);
        }
      }
      errors.push({
        instancePath,
        rulePath: `${rulePath}/maxLength`,
        keyword: "maxLength",
        message: `长度 ${actual} ${unit} 超过上限 ${options.maxLength}（规范化：${options.normalization}）`,
        range,
        excerpt: range ? excerptFor(value, range) : undefined,
      });
    }
  }
}

function codepointRange(normalized: string, skipCodePoints: number): TextRange {
  let start = 0;
  let seen = 0;
  for (const _char of normalized) {
    if (seen === skipCodePoints) {
      break;
    }
    start += _char.length;
    seen += 1;
  }
  return { start, end: normalized.length };
}

function validateValue(
  value: Json,
  schema: CompiledSchema,
  instancePath: string,
  rulePath: string,
  errors: ValidationError[],
): void {
  if (schema.types !== null && !matchesType(value, schema.types)) {
    errors.push({
      instancePath,
      rulePath: `${rulePath}/type`,
      keyword: "type",
      message: `实例类型为 ${instanceTypeName(value)}，不在允许集合 [${schema.types.join(", ")}] 中`,
    });
    return;
  }

  if (isConstSet(schema.constValue) && !deepEqual(value, schema.constValue)) {
    errors.push({
      instancePath,
      rulePath: `${rulePath}/const`,
      keyword: "const",
      message: `实例不等于 const 要求的值 ${JSON.stringify(schema.constValue)}`,
    });
  }

  if (schema.enumValues !== null && !schema.enumValues.some((candidate) => deepEqual(value, candidate))) {
    errors.push({
      instancePath,
      rulePath: `${rulePath}/enum`,
      keyword: "enum",
      message: `实例不在 enum 允许的 ${schema.enumValues.length} 个取值中`,
    });
  }

  if (typeof value === "string") {
    validateString(value, schema, instancePath, rulePath, errors);
    return;
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== null && value.length < schema.minItems) {
      errors.push({
        instancePath,
        rulePath: `${rulePath}/minItems`,
        keyword: "minItems",
        message: `数组长度 ${value.length} 小于下限 ${schema.minItems}`,
      });
    }
    if (schema.maxItems !== null && value.length > schema.maxItems) {
      const rejectedIndexes = Array.from(
        { length: value.length - schema.maxItems },
        (_unused, offset) => schema.maxItems! + offset,
      );
      errors.push({
        instancePath,
        rulePath: `${rulePath}/maxItems`,
        keyword: "maxItems",
        message: `数组长度 ${value.length} 超过上限 ${schema.maxItems}；被拒项索引 [${rejectedIndexes.join(", ")}] 仍逐项校验并在下方报告，未被静默删除`,
      });
      // 拒绝不能静默删除：逐个越界元素仍继续校验/报告
      if (schema.items) {
        for (let index = schema.maxItems; index < value.length; index += 1) {
          validateValue(
            value[index],
            schema.items,
            `${instancePath}/${index}`,
            `${rulePath}/items`,
            errors,
          );
        }
      }
    }
    if (schema.items) {
      const limit = schema.maxItems !== null ? Math.min(value.length, schema.maxItems) : value.length;
      for (let index = 0; index < limit; index += 1) {
        validateValue(value[index], schema.items, `${instancePath}/${index}`, `${rulePath}/items`, errors);
      }
    }
    return;
  }

  if (value !== null && typeof value === "object") {
    const obj = value as Record<string, Json>;
    if (schema.required) {
      for (const key of schema.required) {
        if (!Object.prototype.hasOwnProperty.call(obj, key)) {
          errors.push({
            instancePath: childInstancePath(instancePath, key),
            rulePath: `${rulePath}/required`,
            keyword: "required",
            message: `缺少必需属性 "${key}"`,
          });
        }
      }
    }
    if (schema.properties) {
      for (const [key, subSchema] of Object.entries(schema.properties)) {
        if (Object.prototype.hasOwnProperty.call(obj, key)) {
          validateValue(obj[key], subSchema, childInstancePath(instancePath, key), `${rulePath}/properties/${escapePointerToken(key)}`, errors);
        }
      }
    }
  }
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

export class Validator {
  private readonly compiled: CompiledSchema;

  constructor(schema: unknown) {
    this.compiled = compileSchema(schema);
  }

  validate(instance: Json): ValidationResult {
    const errors: ValidationError[] = [];
    validateValue(instance, this.compiled, "#", "#", errors);
    return { valid: errors.length === 0, errors };
  }
}

export { SchemaError } from "./schema.ts";
export { compileSchema } from "./schema.ts";
