export function collectTempRefs(
  value: unknown,
  prefix: string,
  out: Set<string> = new Set(),
): Set<string> {
  if (typeof value === "string") {
    if (value.startsWith(prefix)) out.add(value);
  } else if (Array.isArray(value)) {
    for (const item of value) collectTempRefs(item, prefix, out);
  } else if (value !== null && typeof value === "object") {
    for (const item of Object.values(value)) collectTempRefs(item, prefix, out);
  }
  return out;
}

export function rewriteTempIds(
  value: unknown,
  idMap: ReadonlyMap<string, string>,
): unknown {
  if (typeof value === "string") return idMap.get(value) ?? value;
  if (Array.isArray(value)) return value.map((item) => rewriteTempIds(item, idMap));
  if (value !== null && typeof value === "object") {
    const result: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) {
      result[key] = rewriteTempIds(item, idMap);
    }
    return result;
  }
  return value;
}
