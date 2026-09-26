/** JSON-Pointer-like paths used for both instance paths and rule paths. */

export function formatPath(segments: readonly (string | number)[]): string {
  if (segments.length === 0) return "/";
  return segments.map((seg) => "/" + escapeSegment(String(seg))).join("");
}

function escapeSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, "/").replace(/~0/g, "~");
}

export function parsePath(pointer: string): string[] {
  if (pointer === "" || pointer === "/") return [];
  if (!pointer.startsWith("/")) {
    throw new Error(`invalid path (must start with "/"): ${pointer}`);
  }
  return pointer.slice(1).split("/").map(unescapeSegment);
}
