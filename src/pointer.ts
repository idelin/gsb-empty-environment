/**
 * JSON Pointer（RFC 6901）与受限模式支持。
 * 模式 = 指针，但每个段可以是字面量或单个 "*"（匹配任意一个键/下标）。
 */

export function escapeToken(token: string): string {
  return token.replace(/~/g, "~0").replace(/\//g, "~1");
}

export function unescapeToken(token: string): string {
  return token.replace(/~1/g, "/").replace(/~0/g, "~");
}

/** 把段数组编译成指针，例如 ["a","b"] -> "/a/b"，[] -> ""。 */
export function compilePointer(segments: string[]): string {
  if (segments.length === 0) return "";
  return "/" + segments.map(escapeToken).join("/");
}

/** 拆分指针为段；非法指针（~ 转义错误等）抛出异常，由调用方定位。 */
export function parsePointer(path: string): string[] {
  if (path === "") return [];
  if (!path.startsWith("/")) {
    throw new Error(`非法 JSON Pointer（必须为空串或以 / 开头）: ${path}`);
  }
  const rawSegments = path.slice(1).split("/");
  return rawSegments.map((seg) => {
    if (/~(?!0|1)/.test(seg)) {
      throw new Error(`非法 JSON Pointer 转义: ${path}`);
    }
    return unescapeToken(seg);
  });
}

/** 具体叶子指针是否匹配模式。段数必须相同；"*" 匹配任意单段。 */
export function matchPattern(pattern: string, concrete: string): boolean {
  const p = parsePointer(pattern);
  const c = parsePointer(concrete);
  if (p.length !== c.length) return false;
  return p.every((seg, i) => seg === "*" || seg === c[i]);
}

/**
 * 两个模式是否可能相交：
 * 同长度且每个对应段要么相等、要么至少一个是 "*"。
 */
export function patternsOverlap(a: string, b: string): boolean {
  const pa = parsePointer(a);
  const pb = parsePointer(b);
  if (pa.length !== pb.length) return false;
  return pa.every((seg, i) => seg === "*" || pb[i] === "*" || seg === pb[i]);
}

/** 用代表段 "0" 替换 "*"，得到分组用的代表模式。 */
export function representativePattern(pattern: string): string {
  return compilePointer(parsePointer(pattern).map((s) => (s === "*" ? "0" : s)));
}

/** 深度优先遍历全部叶子（非对象/数组的值），数组按下标有序访问。 */
export function walkLeaves(
  root: unknown,
  visit: (path: string, segments: string[], value: unknown, parent: unknown, key: string | number | null) => void,
): void {
  const rec = (node: unknown, segs: string[], parent: unknown, key: string | number | null): void => {
    if (node !== null && typeof node === "object") {
      if (Array.isArray(node)) {
        node.forEach((child, i) => rec(child, [...segs, String(i)], node, i));
        return;
      }
      // 对象键无序：排序仅为稳定输出，不影响语义。
      for (const k of Object.keys(node as Record<string, unknown>).sort()) {
        rec((node as Record<string, unknown>)[k], [...segs, k], node, k);
      }
      return;
    }
    visit(compilePointer(segs), segs, node, parent, key);
  };
  rec(root, [], null, null);
}

/** 指针取值；不存在返回 undefined。 */
export function getByPointer(root: unknown, path: string): unknown {
  const segs = parsePointer(path);
  let node: unknown = root;
  for (const seg of segs) {
    if (node === null || typeof node !== "object") return undefined;
    if (Array.isArray(node)) {
      const idx = Number(seg);
      if (!Number.isInteger(idx) || idx < 0 || idx >= node.length) return undefined;
      node = node[idx];
    } else {
      if (!Object.prototype.hasOwnProperty.call(node, seg)) return undefined;
      node = (node as Record<string, unknown>)[seg];
    }
  }
  return node;
}

/** 原地设置指针位置的值（父结构必须存在）。 */
export function setByPointer(root: unknown, path: string, value: unknown): void {
  const segs = parsePointer(path);
  if (segs.length === 0) throw new Error("不能替换根节点");
  let node: any = root;
  for (let i = 0; i < segs.length - 1; i++) {
    const seg = segs[i];
    node = Array.isArray(node) ? node[Number(seg)] : node[seg];
  }
  const last = segs[segs.length - 1];
  if (Array.isArray(node)) node[Number(last)] = value;
  else node[last] = value;
}
