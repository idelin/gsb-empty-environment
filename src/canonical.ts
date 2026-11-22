import { createHash } from "node:crypto";

export function canonicalize(value: unknown): string {
  return JSON.stringify(canonicalValue(value));
}

function canonicalValue(value: unknown): unknown {
  if (value === null || typeof value !== "object") {
    return value;
  }
  if (value instanceof Date) {
    return value.toISOString();
  }
  if (Array.isArray(value)) {
    return value.map((item) => canonicalValue(item));
  }
  const input = value as Record<string, unknown>;
  const output: Record<string, unknown> = {};
  for (const key of Object.keys(input).sort()) {
    if (input[key] !== undefined) {
      output[key] = canonicalValue(input[key]);
    }
  }
  return output;
}

export function sha256Hex(value: unknown): string {
  const input = typeof value === "string" ? value : canonicalize(value);
  return createHash("sha256").update(input, "utf8").digest("hex");
}

