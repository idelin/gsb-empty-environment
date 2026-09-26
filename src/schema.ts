/**
 * 本项目承诺支持的 JSON Schema 子集，以及编译期检查。
 *
 * 设计原则：
 * - 只实现这里声明的关键字；编译期遇到未知关键字直接抛 SchemaError，
 *   绝不静默忽略，避免“以为校验了其实没校验”。
 * - 校验器实例校验阶段不使用任何现成 JSON Schema 验证库。
 */

import type { LengthUnit, NormalizationForm } from "./unicode.ts";

export type Json =
  | null
  | boolean
  | number
  | string
  | Json[]
  | { [key: string]: Json };

export type InstanceType =
  | "null"
  | "boolean"
  | "number"
  | "integer"
  | "string"
  | "array"
  | "object";

export type BidiControlPolicy = "allow" | "deny";

/**
 * 支持的关键字（按用途）：
 *
 * 通用：type（字符串或字符串数组）、const、enum、title、description、$comment
 * 字符串：normalization、allowedScripts、bidiControl、
 *         minLength/maxLength（配合 minLengthUnit/maxLengthUnit）
 * 对象：properties、required
 * 数组：items、minItems、maxItems
 */
export interface SchemaObject {
  type?: InstanceType | InstanceType[];
  const?: Json;
  enum?: Json[];

  normalization?: NormalizationForm;
  allowedScripts?: string[];
  bidiControl?: BidiControlPolicy;
  minLength?: number;
  maxLength?: number;
  minLengthUnit?: LengthUnit;
  maxLengthUnit?: LengthUnit;

  properties?: Record<string, SchemaObject>;
  required?: string[];

  items?: SchemaObject;
  minItems?: number;
  maxItems?: number;

  title?: string;
  description?: string;
  $comment?: string;
}

const KNOWN_KEYWORDS = new Set<string>([
  "type", "const", "enum",
  "normalization", "allowedScripts", "bidiControl",
  "minLength", "maxLength", "minLengthUnit", "maxLengthUnit",
  "properties", "required",
  "items", "minItems", "maxItems",
  "title", "description", "$comment",
]);

const INSTANCE_TYPES = new Set<InstanceType>([
  "null", "boolean", "number", "integer", "string", "array", "object",
]);

const NORMALIZATION_FORMS = new Set<NormalizationForm>([
  "none", "NFC", "NFD", "NFKC", "NFKD",
]);

const LENGTH_UNITS = new Set<LengthUnit>(["codepoints", "graphemes", "utf16"]);

const BIDI_POLICIES = new Set<BidiControlPolicy>(["allow", "deny"]);

/** Schema 自身错误（编译期）。rulePath 为出错关键字在 schema 中的 JSON Pointer。 */
export class SchemaError extends Error {
  readonly rulePath: string;
  constructor(rulePath: string, message: string) {
    super(`Schema 错误 @ ${rulePath}: ${message}`);
    this.name = "SchemaError";
    this.rulePath = rulePath;
  }
}

export interface CompiledStringOptions {
  normalization: NormalizationForm;
  allowedScripts: string[];
  scriptMatchers: RegExp[];
  bidiControl: BidiControlPolicy;
  minLength: number | null;
  maxLength: number | null;
  minLengthUnit: LengthUnit;
  maxLengthUnit: LengthUnit;
}

export interface CompiledSchema {
  raw: SchemaObject;
  types: InstanceType[] | null;
  constValue: Json | undefined;
  enumValues: Json[] | null;
  stringOptions: CompiledStringOptions | null;
  properties: Record<string, CompiledSchema> | null;
  required: string[] | null;
  items: CompiledSchema | null;
  minItems: number | null;
  maxItems: number | null;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function fail(rulePath: string, message: string): never {
  throw new SchemaError(rulePath, message);
}

function compileType(value: unknown, rulePath: string): InstanceType[] | null {
  if (value === undefined) {
    return null;
  }
  const list = Array.isArray(value) ? value : [value];
  if (list.length === 0) {
    fail(rulePath, "type 不能为空数组");
  }
  for (const entry of list) {
    if (typeof entry !== "string" || !INSTANCE_TYPES.has(entry as InstanceType)) {
      fail(rulePath, `不支持的类型取值：${JSON.stringify(entry)}`);
    }
  }
  return list as InstanceType[];
}

function compileNonNegativeInteger(value: unknown, rulePath: string): number | null {
  if (value === undefined) {
    return null;
  }
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
    fail(rulePath, "必须是非负整数");
  }
  return value as number;
}

function compileStringOptions(schema: SchemaObject, rulePath: string): CompiledStringOptions {
  const normalization = schema.normalization ?? "none";
  if (!NORMALIZATION_FORMS.has(normalization)) {
    fail(`${rulePath}/normalization`, `不支持的规范化形式：${String(normalization)}`);
  }

  let allowedScripts: string[] = [];
  let scriptMatchers: RegExp[] = [];
  if (schema.allowedScripts !== undefined) {
    if (
      !Array.isArray(schema.allowedScripts) ||
      schema.allowedScripts.some((entry) => typeof entry !== "string")
    ) {
      fail(`${rulePath}/allowedScripts`, "必须是脚本名称字符串数组（如 [\"Latin\", \"Han\"]）");
    }
    allowedScripts = [...schema.allowedScripts];
    for (const name of allowedScripts) {
      try {
        scriptMatchers.push(new RegExp(`\\p{sc=${name}}`, "u"));
      } catch {
        fail(`${rulePath}/allowedScripts`, `未知或不可用的 Unicode 脚本名称：${name}`);
      }
    }
  }

  const bidiControl = schema.bidiControl ?? "deny";
  if (!BIDI_POLICIES.has(bidiControl)) {
    fail(`${rulePath}/bidiControl`, "只允许 \"allow\" 或 \"deny\"");
  }

  const minLengthUnit = schema.minLengthUnit ?? "codepoints";
  const maxLengthUnit = schema.maxLengthUnit ?? "codepoints";
  if (!LENGTH_UNITS.has(minLengthUnit)) {
    fail(`${rulePath}/minLengthUnit`, "只允许 codepoints | graphemes | utf16");
  }
  if (!LENGTH_UNITS.has(maxLengthUnit)) {
    fail(`${rulePath}/maxLengthUnit`, "只允许 codepoints | graphemes | utf16");
  }

  return {
    normalization,
    allowedScripts,
    scriptMatchers,
    bidiControl,
    minLength: compileNonNegativeInteger(schema.minLength, `${rulePath}/minLength`),
    maxLength: compileNonNegativeInteger(schema.maxLength, `${rulePath}/maxLength`),
    minLengthUnit,
    maxLengthUnit,
  };
}

export function compileSchema(raw: unknown, rulePath = "#"): CompiledSchema {
  if (!isPlainObject(raw)) {
    fail(rulePath, "schema 必须是对象");
  }
  const schema = raw as SchemaObject;

  for (const key of Object.keys(schema)) {
    if (!KNOWN_KEYWORDS.has(key)) {
      fail(`${rulePath}/${escapePointerToken(key)}`, `子集不支持的关键字：${key}`);
    }
  }

  const types = compileType(schema.type, `${rulePath}/type`);

  let enumValues: Json[] | null = null;
  if (schema.enum !== undefined) {
    if (!Array.isArray(schema.enum)) {
      fail(`${rulePath}/enum`, "必须是数组");
    }
    enumValues = schema.enum;
  }

  if (
    schema.minLength !== undefined || schema.maxLength !== undefined ||
    schema.normalization !== undefined || schema.allowedScripts !== undefined ||
    schema.bidiControl !== undefined ||
    schema.minLengthUnit !== undefined || schema.maxLengthUnit !== undefined
  ) {
    if (types !== null && !types.includes("string")) {
      fail(rulePath, "字符串专用关键字只能用于声明了 type: \"string\" 的 schema");
    }
  }
  if (schema.properties !== undefined || schema.required !== undefined) {
    if (types !== null && !types.includes("object")) {
      fail(rulePath, "对象专用关键字只能用于声明了 type: \"object\" 的 schema");
    }
  }
  if (schema.items !== undefined || schema.minItems !== undefined || schema.maxItems !== undefined) {
    if (types !== null && !types.includes("array")) {
      fail(rulePath, "数组专用关键字只能用于声明了 type: \"array\" 的 schema");
    }
  }

  let properties: Record<string, CompiledSchema> | null = null;
  if (schema.properties !== undefined) {
    if (!isPlainObject(schema.properties)) {
      fail(`${rulePath}/properties`, "必须是对象");
    }
    properties = {};
    for (const [key, sub] of Object.entries(schema.properties)) {
      properties[key] = compileSchema(sub, `${rulePath}/properties/${escapePointerToken(key)}`);
    }
  }

  let required: string[] | null = null;
  if (schema.required !== undefined) {
    if (
      !Array.isArray(schema.required) ||
      schema.required.some((entry) => typeof entry !== "string")
    ) {
      fail(`${rulePath}/required`, "必须是字符串数组");
    }
    required = [...schema.required];
  }

  let items: CompiledSchema | null = null;
  if (schema.items !== undefined) {
    items = compileSchema(schema.items, `${rulePath}/items`);
  }

  return {
    raw: schema,
    types,
    constValue: schema.const,
    enumValues,
    stringOptions: compileStringOptions(schema, rulePath),
    properties,
    required,
    items,
    minItems: compileNonNegativeInteger(schema.minItems, `${rulePath}/minItems`),
    maxItems: compileNonNegativeInteger(schema.maxItems, `${rulePath}/maxItems`),
  };
}

export function isConstSet(value: CompiledSchema["constValue"]): value is Json {
  return value !== undefined;
}

export function escapePointerToken(token: string): string {
  return token.replace(/~/g, "~0").replace(/\//g, "~1");
}
