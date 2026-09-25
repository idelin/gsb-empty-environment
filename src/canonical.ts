import { createHash } from "node:crypto";
import type { JsonValue } from "./types.ts";
import { ExpandError } from "./errors.ts";

/**
 * 规范化序列化：对象键按字典序排序（对象键无序），数组保持顺序（数组有序）。
 * 同一语义树无论键序如何都得到同一字符串。
 */
export function canonicalize(value: JsonValue): string {
  if (value === null) return "null";
  switch (typeof value) {
    case "boolean":
    case "number":
      return JSON.stringify(value);
    case "string":
      return JSON.stringify(value);
    case "object": {
      if (Array.isArray(value)) {
        return "[" + value.map(canonicalize).join(",") + "]";
      }
      const keys = Object.keys(value).sort();
      const parts = keys.map(
        (k) => JSON.stringify(k) + ":" + canonicalize(value[k]!)
      );
      return "{" + parts.join(",") + "}";
    }
    default:
      throw new ExpandError("$", `value is not JSON-compatible: ${String(value)}`);
  }
}

/** 输入摘要：模板 + 参数 + 身份声明 的 SHA-256，用于游标绑定版本。 */
export function digestOf(value: JsonValue): string {
  return createHash("sha256").update(canonicalize(value)).digest("hex");
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  return canonicalize(a) === canonicalize(b);
}
