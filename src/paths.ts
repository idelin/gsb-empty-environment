/** Minimal JSON-pointer-ish path helpers ("/data/assignee", "/data/tags/0"). */

export function joinPath(base: string, segment: string | number): string {
  const seg = String(segment).replace(/~/g, "~0").replace(/\//g, "~1");
  return base === "" ? `/${seg}` : `${base}/${seg}`;
}

export function getAtPath(root: unknown, path: string): unknown {
  if (path === "" || path === "/") return root;
  let cur: unknown = root;
  for (const rawSeg of path.split("/").slice(1)) {
    const seg = rawSeg.replace(/~1/g, "/").replace(/~0/g, "~");
    if (cur === null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}
