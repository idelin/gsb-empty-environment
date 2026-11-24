/**
 * Small deterministic JSON serializer and non-cryptographic digest.
 *
 * The digest is only used to bind a certificate to the exact normalized graph
 * it was produced from.  It is not a security primitive.
 */

export function stableJson(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map((entry) => sortValue(entry));
  }
  if (value !== null && typeof value === "object") {
    const input = value as Record<string, unknown>;
    const output: Record<string, unknown> = {};
    for (const key of Object.keys(input).sort()) {
      const entry = input[key];
      if (entry !== undefined) {
        output[key] = sortValue(entry);
      }
    }
    return output;
  }
  return value;
}

/** FNV-1a, 32 bit, rendered as eight lowercase hex digits. */
export function digestString(text: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(16).padStart(8, "0");
}

export function digestValue(value: unknown): string {
  return digestString(stableJson(value));
}
