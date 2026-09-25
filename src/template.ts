import { ExpandError } from "./errors.ts";
import {
  canonicalize,
  parsePath,
  pathJoin,
  type JsonValue,
  type PathSegment,
} from "./json.ts";
import type { Assignment } from "./constraints.ts";

/** Deep-render a template, substituting `{ "$param": name }` nodes. */
export function renderTemplate(
  node: JsonValue,
  assignment: Assignment,
  path = "$",
): JsonValue {
  if (node === null || typeof node !== "object") return node;
  if (Array.isArray(node)) {
    return node.map((item, i) => renderTemplate(item, assignment, pathJoin(path, i)));
  }
  const obj = node as Record<string, JsonValue>;
  if ("$param" in obj) {
    const name = obj.$param as string;
    if (!assignment.has(name)) {
      throw new ExpandError(
        "template",
        pathJoin(path, "$param"),
        `parameter '${name}' has no assigned value`,
      );
    }
    return assignment.get(name)!;
  }
  const out: Record<string, JsonValue> = {};
  for (const [key, value] of Object.entries(obj)) {
    out[key] = renderTemplate(value, assignment, pathJoin(path, key));
  }
  return out;
}

export class IdentityExtractor {
  private readonly parsed: PathSegment[][];
  private readonly display: readonly string[];

  constructor(paths: readonly string[]) {
    this.parsed = paths.map(parsePath);
    this.display = paths;
  }

  get declared(): boolean {
    return this.parsed.length > 0;
  }

  /** Canonical key for the declared identity of a record. */
  keyOf(record: JsonValue): string {
    const parts: JsonValue[] = this.parsed.map((segments, i) => {
      let node: JsonValue = record;
      for (const seg of segments) {
        if (node === null || typeof node !== "object") {
          throw new ExpandError(
            "identity",
            this.display[i],
            "identity path does not exist in record",
          );
        }
        node = Array.isArray(node)
          ? (node as JsonValue[])[seg as number]
          : (node as Record<string, JsonValue>)[seg as string];
        if (node === undefined) {
          throw new ExpandError(
            "identity",
            this.display[i],
            "identity path does not exist in record",
          );
        }
      }
      return node;
    });
    return canonicalize(parts);
  }
}
