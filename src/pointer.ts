import type { JsonValue } from "./schema.ts";

export function isPlainObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function escapeSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, "/").replace(/~0/g, "~");
}

export function toPointer(segments: string[]): string {
  return segments.map((s) => "/" + escapeSegment(s)).join("");
}

export function parsePointer(pointer: string): string[] {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) {
    throw new Error(`invalid JSON pointer: ${JSON.stringify(pointer)}`);
  }
  return pointer.slice(1).split("/").map(unescapeSegment);
}

export function getAt(root: JsonValue, segments: string[]): { found: boolean; value?: JsonValue } {
  let node: JsonValue = root;
  for (const segment of segments) {
    if (isPlainObject(node)) {
      if (!(segment in node)) return { found: false };
      node = node[segment];
    } else if (Array.isArray(node)) {
      const index = Number(segment);
      if (!Number.isInteger(index) || index < 0 || index >= node.length) return { found: false };
      node = node[index];
    } else {
      return { found: false };
    }
  }
  return { found: true, value: node };
}

export function setAt(root: JsonValue, segments: string[], value: JsonValue): void {
  if (segments.length === 0) throw new Error("cannot replace the document root");
  const parent = getAt(root, segments.slice(0, -1));
  if (!parent.found) throw new Error(`parent of /${segments.join("/")} does not exist`);
  const key = segments[segments.length - 1];
  const target = parent.value!;
  if (isPlainObject(target)) {
    target[key] = value;
  } else if (Array.isArray(target)) {
    const index = Number(key);
    if (!Number.isInteger(index) || index < 0 || index > target.length) {
      throw new Error(`invalid array index ${key}`);
    }
    target[index] = value;
  } else {
    throw new Error(`cannot set ${key} on a scalar`);
  }
}
