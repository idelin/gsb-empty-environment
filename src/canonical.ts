/**
 * Deterministic serialization for signature payloads.
 * Object keys are sorted; all arrays (including policy value sets) are sorted
 * after primitive normalization, so semantically identical documents match.
 */
export function canonicalize(value: unknown): string {
  return JSON.stringify(normalize(value));
}

function normalize(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(normalize).sort((a, b) => stableCompare(a, b));
  }
  if (value !== null && typeof value === "object") {
    const obj = value as Record<string, unknown>;
    const result: Record<string, unknown> = {};
    for (const key of Object.keys(obj).sort()) {
      result[key] = normalize(obj[key]);
    }
    return result;
  }
  return value;
}

function stableCompare(a: unknown, b: unknown): number {
  const ka = keyOf(a);
  const kb = keyOf(b);
  return ka < kb ? -1 : ka > kb ? 1 : 0;
}

function keyOf(value: unknown): string {
  const tag = Array.isArray(value) ? "A" : value !== null && typeof value === "object" ? "O" : "P";
  return tag + ":" + JSON.stringify(value);
}
