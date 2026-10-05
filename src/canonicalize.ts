/**
 * Deterministic, type-tagged serialization for algorithm params.
 * Object keys are sorted, so {a:1,b:2} and {b:2,a:1} map to the same key,
 * while 1 and "1" (or [1,2] and Float64Array[1,2]) never collide.
 */
export function canonicalize(value: unknown): string {
  return encode(value, new Set<object>());
}

function encode(value: unknown, seen: Set<object>): string {
  if (value === undefined) return "u";
  if (value === null) return "n";
  if (typeof value === "boolean") return value ? "b:t" : "b:f";
  if (typeof value === "number") {
    if (Number.isNaN(value)) return "N:NaN";
    if (Object.is(value, -0)) return "N:-0";
    return `N:${String(value)}`;
  }
  if (typeof value === "bigint") return `B:${value.toString()}`;
  if (typeof value === "string") return `s:${JSON.stringify(value)}`;
  if (typeof value === "function" || typeof value === "symbol") {
    throw new TypeError("params must be plain data (no functions/symbols)");
  }
  if (typeof value === "object") {
    if (seen.has(value)) {
      throw new TypeError("params must not contain circular references");
    }
    seen.add(value);
    try {
      if (Array.isArray(value)) {
        return `a:[${value.map((item) => encode(item, seen)).join(",")}]`;
      }
      if (ArrayBuffer.isView(value)) {
        const tag = value.constructor.name;
        const items = Array.from(value as unknown as ArrayLike<unknown>);
        return `T:${tag}:[${items.map((item) => encode(item, seen)).join(",")}]`;
      }
      const keys = Object.keys(value).sort();
      const body = keys
        .map(
          (key) =>
            `${JSON.stringify(key)}=${encode(
              (value as Record<string, unknown>)[key],
              seen,
            )}`,
        )
        .join(",");
      return `o:{${body}}`;
    } finally {
      seen.delete(value);
    }
  }
  throw new TypeError(`unsupported param type: ${typeof value}`);
}
