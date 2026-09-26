import { createHash } from "node:crypto";

/** Canonical JSON: object keys sorted recursively so digests are stable. */
export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value) ?? "undefined";
  if (Array.isArray(value)) return "[" + value.map(canonicalize).join(",") + "]";
  const obj = value as Record<string, unknown>;
  const body = Object.keys(obj)
    .sort()
    .map((k) => JSON.stringify(k) + ":" + canonicalize(obj[k]))
    .join(",");
  return "{" + body + "}";
}

/** Full sha256 hex digest of the canonical form. */
export function digest(value: unknown): string {
  return createHash("sha256").update(canonicalize(value), "utf8").digest("hex");
}

/** Short digest for display and cache file names. */
export function shortDigest(value: unknown): string {
  return digest(value).slice(0, 16);
}
