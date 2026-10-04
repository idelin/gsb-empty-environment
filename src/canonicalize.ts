/**
 * 参数规范化：把算法参数编码为与键序无关、结构唯一的字符串。
 *
 * - 对象键排序，因此 {a:1,b:2} 与 {b:2,a:1} 得到同一编码（应当命中）；
 * - 类型敏感，1 与 "1"、[] 与 {} 编码不同（参数变了绝不能误命中）；
 * - 支持嵌套对象/数组、TypedArray、bigint、NaN、Infinity；
 * - 函数、symbol、循环引用直接拒绝：无法规范化的参数不允许进缓存键。
 */
export function canonicalize(value: unknown): string {
  const seen = new Set<object>();

  const encode = (v: unknown): string => {
    if (v === null) return 'null';
    const t = typeof v;
    if (t === 'number') {
      const n = v as number;
      if (Number.isNaN(n)) return 'NaN';
      if (!Number.isFinite(n)) return n > 0 ? 'Infinity' : '-Infinity';
      if (Object.is(n, -0)) return '-0';
      return JSON.stringify(n);
    }
    if (t === 'string') return JSON.stringify(v);
    if (t === 'boolean') return String(v);
    if (t === 'undefined') return 'undefined';
    if (t === 'bigint') return `bigint:${String(v)}`;
    if (t === 'function' || t === 'symbol') {
      throw new TypeError('params must be canonicalizable data: functions and symbols are not allowed');
    }

    const obj = v as object;
    if (seen.has(obj)) {
      throw new TypeError('params must not contain cyclic references');
    }
    seen.add(obj);
    try {
      if (ArrayBuffer.isView(obj)) {
        if (obj instanceof DataView) {
          throw new TypeError('DataView params are not supported; pass a typed array instead');
        }
        const elements = Array.from(obj as unknown as ArrayLike<number | bigint>, encode);
        return `${obj.constructor.name}[${elements.join(',')}]`;
      }
      if (Array.isArray(obj)) {
        return `[${obj.map(encode).join(',')}]`;
      }
      const keys = Object.keys(obj).sort();
      const pairs = keys.map(
        (key) => `${JSON.stringify(key)}:${encode((obj as Record<string, unknown>)[key])}`,
      );
      return `{${pairs.join(',')}}`;
    } finally {
      seen.delete(obj);
    }
  };

  return encode(value);
}
