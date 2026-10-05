export type PathSeg = string | number;
export type Path = readonly PathSeg[];

export function escapeSeg(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

export function unescapeSeg(seg: string): string {
  return seg.replace(/~1/g, "/").replace(/~0/g, "~");
}

/** RFC-6901-style encoding; keys containing "/" or "~" stay unambiguous. */
export function encodePath(path: Path): string {
  let out = "";
  for (const seg of path) {
    out += "/" + (typeof seg === "number" ? String(seg) : escapeSeg(seg));
  }
  return out;
}

const BARE_KEY = /^[A-Za-z_$][A-Za-z0-9_$-]*$/;

/** Human-readable rendering, e.g. $.store.book[0]["a/b"]. */
export function formatPath(path: Path): string {
  let out = "$";
  for (const seg of path) {
    if (typeof seg === "number") out += `[${seg}]`;
    else if (BARE_KEY.test(seg)) out += `.${seg}`;
    else out += `[${JSON.stringify(seg)}]`;
  }
  return out;
}

export function isPrefix(prefix: Path, path: Path): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}

export function pathEquals(a: Path, b: Path): boolean {
  return a.length === b.length && isPrefix(a, b);
}
