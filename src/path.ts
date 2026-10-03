export type PathSegment = string | number;

/**
 * Parses a dotted path like `user.address.city` or `items[0].price`
 * into segments: ['user','address','city'] / ['items',0,'price'].
 */
export function parsePath(path: string): PathSegment[] {
  const segments: PathSegment[] = [];
  let token = "";
  let i = 0;
  const flush = (): void => {
    if (token.length > 0) {
      segments.push(token);
      token = "";
    }
  };
  while (i < path.length) {
    const ch = path[i];
    if (ch === ".") {
      flush();
      i += 1;
    } else if (ch === "[") {
      flush();
      const close = path.indexOf("]", i);
      if (close === -1) {
        throw new Error(`Invalid path (missing ']'): ${path}`);
      }
      const rawIndex = path.slice(i + 1, close);
      if (!/^\d+$/.test(rawIndex)) {
        throw new Error(`Invalid array index in path: ${path}`);
      }
      segments.push(Number(rawIndex));
      i = close + 1;
    } else {
      token += ch;
      i += 1;
    }
  }
  flush();
  if (segments.length === 0) {
    throw new Error(`Empty path: ${path}`);
  }
  return segments;
}

/**
 * Canonical string form of a segment list, matching parsePath's format:
 * ['items',0,'price'] -> 'items[0].price'.
 */
export function formatPath(segments: readonly PathSegment[]): string {
  let out = "";
  for (const seg of segments) {
    if (typeof seg === "number") {
      out += `[${seg}]`;
    } else {
      out += out.length === 0 ? seg : `.${seg}`;
    }
  }
  return out;
}
