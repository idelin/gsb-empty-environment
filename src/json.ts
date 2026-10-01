/** JSON-compatible value tree. Object keys are unordered; arrays are ordered. */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

/** RFC 6901 escaping for a single JSON-pointer segment. */
export function escapePointerSegment(seg: string): string {
  return seg.replace(/~/g, "~0").replace(/\//g, "~1");
}

/** Build a JSON pointer ("" for root) from concrete path segments. */
export function pointerOf(segments: string[]): string {
  if (segments.length === 0) return "";
  return "/" + segments.map(escapePointerSegment).join("/");
}
