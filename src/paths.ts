export function joinPtr(base: string, seg: string | number): string {
  const esc = String(seg).replace(/~/g, '~0').replace(/\//g, '~1');
  return `${base}/${esc}`;
}

export function parsePtr(ptr: string): string[] {
  if (ptr === '') return [];
  if (ptr[0] !== '/') throw new Error(`非法 JSON Pointer: ${ptr}`);
  return ptr
    .substring(1)
    .split('/')
    .map((s) => s.replace(/~1/g, '/').replace(/~0/g, '~'));
}

export function ptrToPathExpr(ptr: string): string {
  const segs = parsePtr(ptr);
  if (segs.length === 0) return '$';
  return '$' + segs.map((s) => `[${JSON.stringify(s)}]`).join('');
}

export function deepClone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export function getAtPath(root: unknown, ptr: string): { exists: boolean; value: unknown } {
  const segs = parsePtr(ptr);
  let cur: unknown = root;
  for (const seg of segs) {
    if (!isPlainObject(cur) || !(seg in cur)) return { exists: false, value: undefined };
    cur = cur[seg];
  }
  return { exists: true, value: cur };
}

// 沿路径创建中间对象；若中间节点不是对象则失败
export function setAtPath(root: Record<string, unknown>, ptr: string, value: unknown): void {
  const segs = parsePtr(ptr);
  let cur: Record<string, unknown> = root;
  for (let i = 0; i < segs.length - 1; i++) {
    const seg = segs[i];
    const next = cur[seg];
    if (next === undefined || next === null) {
      const created: Record<string, unknown> = {};
      cur[seg] = created;
      cur = created;
    } else if (isPlainObject(next)) {
      cur = next;
    } else {
      throw new Error(`路径 ${ptrToPathExpr(ptr)} 的中间段 ${JSON.stringify(seg)} 不是对象，无法设置`);
    }
  }
  cur[segs[segs.length - 1]] = value;
}

export function unsetAtPath(root: unknown, ptr: string): boolean {
  const segs = parsePtr(ptr);
  let cur: unknown = root;
  for (let i = 0; i < segs.length - 1; i++) {
    if (!isPlainObject(cur)) return false;
    cur = cur[segs[i]];
  }
  if (!isPlainObject(cur)) return false;
  const last = segs[segs.length - 1];
  if (!(last in cur)) return false;
  delete cur[last];
  return true;
}
