export function splitPath(path: string): string[] {
  const segments: string[] = [];
  for (const segment of path.split("/")) {
    if (segment === "" || segment === ".") continue;
    segments.push(segment);
  }
  return segments;
}

export function canonicalPath(path: string): string {
  const out: string[] = [];
  for (const segment of splitPath(path)) {
    if (segment === "..") {
      out.pop();
      continue;
    }
    out.push(segment);
  }
  return "/" + out.join("/");
}

export function normalizePath(path: string): string {
  return "/" + splitPath(path).join("/");
}

export function isPrefixPath(prefix: string, path: string): boolean {
  if (prefix === "/") return true;
  return path === prefix || path.startsWith(prefix + "/");
}
