export type Segments = string[];

export function parsePointer(pointer: string): Segments {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) {
    throw new Error(`invalid JSON pointer: ${pointer}`);
  }
  return pointer.slice(1).split("/").map(unescapeSegment);
}

export function formatPointer(segments: Segments): string {
  if (segments.length === 0) return "";
  return "/" + segments.map(escapeSegment).join("/");
}

export function isPrefix(prefix: Segments, path: Segments): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}

export function escapeSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, "/").replace(/~0/g, "~");
}
