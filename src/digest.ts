import { createHash } from "node:crypto";

/** Canonical JSON: object keys sorted recursively so equal schemas hash equally. */
function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(",")}]`;
  }
  const entries = Object.keys(value as Record<string, unknown>)
    .sort()
    .map(
      (key) =>
        `${JSON.stringify(key)}:${canonicalize((value as Record<string, unknown>)[key])}`,
    );
  return `{${entries.join(",")}}`;
}

/** Stable content digest of a schema; used to detect same-version conflicts. */
export function schemaDigest(schema: unknown): string {
  return createHash("sha256").update(canonicalize(schema)).digest("hex").slice(0, 16);
}
