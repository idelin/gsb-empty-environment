/**
 * 临时 id 引用的深度扫描与替换。
 * payload 视为 JSON 树：字符串叶子若命中 idMap 中的临时 id，则替换为服务器 id。
 */

export function rewriteRefs(
  value: unknown,
  idMap: Readonly<Record<string, string>>,
): unknown {
  if (typeof value === 'string') return idMap[value] ?? value;
  if (Array.isArray(value)) return value.map((item) => rewriteRefs(item, idMap));
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = rewriteRefs(item, idMap);
    }
    return out;
  }
  return value;
}

export function collectRefs(value: unknown, into: Set<string> = new Set()): Set<string> {
  if (typeof value === 'string') {
    into.add(value);
  } else if (Array.isArray(value)) {
    for (const item of value) collectRefs(item, into);
  } else if (value !== null && typeof value === 'object') {
    for (const item of Object.values(value)) collectRefs(item, into);
  }
  return into;
}
