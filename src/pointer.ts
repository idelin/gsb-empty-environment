export function escapePointerSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** Build a JSON pointer (RFC 6901) from path segments. Root is "". */
export function toPointer(path: string[]): string {
  if (path.length === 0) return "";
  return "/" + path.map(escapePointerSegment).join("/");
}

/** Read the value at a path of segments. Returns undefined when absent. */
export function getAtPath(value: unknown, path: string[]): unknown {
  let current: unknown = value;
  for (const segment of path) {
    if (current === null || typeof current !== "object") return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}
