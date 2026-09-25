import type { JsonValue } from "./json.ts";

/** RFC 6901 JSON Pointer utilities. Every diagnostic path is a pointer. */

function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, "/").replace(/~0/g, "~");
}

function escapeSegment(segment: string): string {
  return segment.replace(/~/g, "~0").replace(/\//g, "~1");
}

export function parsePointer(pointer: string): string[] {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) {
    throw new Error(`Invalid JSON Pointer: ${JSON.stringify(pointer)}`);
  }
  return pointer.slice(1).split("/").map(unescapeSegment);
}

export function formatPointer(segments: string[]): string {
  return segments.map((segment) => "/" + escapeSegment(segment)).join("");
}

function childAt(node: JsonValue, segment: string): JsonValue {
  if (node === null || typeof node !== "object") {
    throw new Error(`Cannot descend into non-object at segment ${JSON.stringify(segment)}`);
  }
  if (Array.isArray(node)) {
    if (!/^(0|[1-9]\d*)$/.test(segment)) {
      throw new Error(`Invalid array index ${JSON.stringify(segment)}`);
    }
    const index = Number(segment);
    if (index >= node.length) {
      throw new Error(`Array index out of bounds: ${index}`);
    }
    return node[index];
  }
  if (!Object.prototype.hasOwnProperty.call(node, segment)) {
    throw new Error(`Missing key ${JSON.stringify(segment)}`);
  }
  return (node as Record<string, JsonValue>)[segment];
}

export function hasAt(root: JsonValue, pointer: string): boolean {
  try {
    getAt(root, pointer);
    return true;
  } catch {
    return false;
  }
}

export function getAt(root: JsonValue, pointer: string): JsonValue {
  let node = root;
  for (const segment of parsePointer(pointer)) {
    node = childAt(node, segment);
  }
  return node;
}

/** Mutates `root` in place; the engine only ever mutates its private working copy. */
export function setAt(root: JsonValue, pointer: string, value: JsonValue): void {
  const segments = parsePointer(pointer);
  if (segments.length === 0) {
    throw new Error("Replacing the document root is not supported");
  }
  let node = root;
  for (const segment of segments.slice(0, -1)) {
    node = childAt(node, segment);
  }
  const last = segments[segments.length - 1];
  if (node === null || typeof node !== "object") {
    throw new Error(`Cannot write into non-object at ${JSON.stringify(pointer)}`);
  }
  if (Array.isArray(node)) {
    if (!/^(0|[1-9]\d*)$/.test(last)) {
      throw new Error(`Invalid array index ${JSON.stringify(last)}`);
    }
    const index = Number(last);
    if (index >= node.length) {
      throw new Error(`Array index out of bounds: ${index}`);
    }
    node[index] = value;
  } else {
    (node as Record<string, JsonValue>)[last] = value;
  }
}
