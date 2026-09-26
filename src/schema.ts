import { createHash } from "node:crypto";

/**
 * 本库只承诺实现以下 JSON Schema 子集（见 README「Schema 子集承诺」）：
 * type / properties / required / additionalProperties / items /
 * minItems / maxItems / enum / const / minimum / maximum /
 * exclusiveMinimum / exclusiveMaximum / minLength / maxLength / pattern
 *
 * 未列出的关键字一律不解析、不生效，也不报错——它们不属于承诺的子集。
 */
export type SchemaType =
  | "object"
  | "array"
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "null";

export interface Schema {
  type?: SchemaType | SchemaType[];
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean | Schema;
  items?: Schema;
  minItems?: number;
  maxItems?: number;
  enum?: unknown[];
  const?: unknown;
  minimum?: number;
  maximum?: number;
  exclusiveMinimum?: number;
  exclusiveMaximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
}

/** 深度规范化：对象键排序后序列化，保证同一语义 schema 得到同一摘要。 */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value) ?? "undefined";
  }
  if (Array.isArray(value)) {
    return "[" + value.map(canonicalize).join(",") + "]";
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const body = keys
    .map((key) => JSON.stringify(key) + ":" + canonicalize(record[key]))
    .join(",");
  return "{" + body + "}";
}

/** schema 内容摘要（sha256），用于检测「同版本号、不同内容」的冲突。 */
export function schemaDigest(schema: Schema): string {
  return createHash("sha256").update(canonicalize(schema), "utf8").digest("hex");
}
