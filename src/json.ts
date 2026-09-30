/** JSON 兼容树：对象键无序、数组有序。 */
export type JSONValue =
  | null
  | boolean
  | number
  | string
  | JSONValue[]
  | { [key: string]: JSONValue };

export function isJSONObject(v: JSONValue): v is { [key: string]: JSONValue } {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** 规范化序列化：对象键排序（无序语义），数组保持顺序（有序语义）。 */
export function canonical(v: JSONValue): string {
  if (v === null) return 'null';
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']';
  if (typeof v === 'object') {
    const keys = Object.keys(v).sort();
    return '{' + keys.map((k) => JSON.stringify(k) + ':' + canonical(v[k]!)).join(',') + '}';
  }
  return JSON.stringify(v);
}

/** 深比较：对象键顺序无关，数组顺序相关。 */
export function deepEqual(a: JSONValue, b: JSONValue): boolean {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((x, i) => deepEqual(x, b[i]!));
  }
  if (isJSONObject(a) && isJSONObject(b)) {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    return ka.length === kb.length && ka.every((k) => k in b && deepEqual(a[k]!, b[k]!));
  }
  return false;
}

export type PathSeg = string | number;

/** 解析形如 $.a.b[0] 的路径。 */
export function parsePath(path: string): PathSeg[] {
  if (!path.startsWith('$')) throw new Error(`路径必须以 $ 开头: ${path}`);
  const segs: PathSeg[] = [];
  let i = 1;
  while (i < path.length) {
    if (path[i] === '.') {
      const m = /^\.([A-Za-z_][A-Za-z0-9_]*)/.exec(path.slice(i));
      if (!m) throw new Error(`非法路径: ${path}`);
      segs.push(m[1]!);
      i += m[0].length;
    } else if (path[i] === '[') {
      const m = /^\[(\d+)\]/.exec(path.slice(i));
      if (!m) throw new Error(`非法路径: ${path}`);
      segs.push(Number(m[1]));
      i += m[0].length;
    } else {
      throw new Error(`非法路径: ${path}`);
    }
  }
  return segs;
}

export function formatPath(segs: PathSeg[]): string {
  let s = '$';
  for (const seg of segs) s += typeof seg === 'number' ? `[${seg}]` : `.${seg}`;
  return s;
}

export function getPath(root: JSONValue, segs: PathSeg[]): { found: boolean; value?: JSONValue } {
  let cur: JSONValue = root;
  for (const seg of segs) {
    if (typeof seg === 'number') {
      if (!Array.isArray(cur) || seg >= cur.length) return { found: false };
      cur = cur[seg]!;
    } else {
      if (!isJSONObject(cur) || !(seg in cur)) return { found: false };
      cur = cur[seg]!;
    }
  }
  return { found: true, value: cur };
}
