import { createHash } from "node:crypto";

export function sha256Hex(data: string): string {
  return createHash("sha256").update(data, "utf8").digest("hex");
}

/** 确定性 JSON 序列化（对象键递归排序），用于哈希前的规范化。 */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value) ?? "null";
  }
  if (Array.isArray(value)) {
    return "[" + value.map((item) => canonicalize(item)).join(",") + "]";
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  const parts = keys.map(
    (key) => JSON.stringify(key) + ":" + canonicalize(record[key]),
  );
  return "{" + parts.join(",") + "}";
}

export function hashCanonical(value: unknown): string {
  return sha256Hex(canonicalize(value));
}
