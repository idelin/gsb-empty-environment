/** FNV-1a 32-bit hash over a string — deterministic across runs/platforms. */
export function fnv1a(input: string): number {
  let h = 0x811c_9dc5;
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 0x0100_0193);
  }
  return h >>> 0;
}

/** Stable stringify: object keys sorted so equal states hash equally. */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") {
    if (typeof value === "bigint") return `"${value}n"`;
    return JSON.stringify(value) ?? "undefined";
  }
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(",")}]`;
  }
  if (value instanceof Uint8Array) {
    return `"u8:${Array.from(value as Uint8Array).join(".")}"`;
  }
  const keys = Object.keys(value as Record<string, unknown>).sort();
  const body = keys
    .map((k) => `${JSON.stringify(k)}:${stableStringify((value as Record<string, unknown>)[k])}`)
    .join(",");
  return `{${body}}`;
}

export function hashState(value: unknown): number {
  return fnv1a(stableStringify(value));
}

/** Mix two 32-bit hashes (order-sensitive). */
export function mix(a: number, b: number): number {
  let h = a ^ b;
  h = Math.imul(h ^ (h >>> 16), 0x45d9_f3b);
  h = Math.imul(h ^ (h >>> 16), 0x45d9_f3b);
  return (h ^ (h >>> 16)) >>> 0;
}
