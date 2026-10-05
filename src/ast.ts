import type { PathSeg } from "./path.ts";

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export type SimpleSegment =
  | { kind: "field"; name: string }
  | { kind: "index"; index: number }
  | { kind: "slice"; start: number | null; end: number | null; step: number | null }
  | { kind: "wildcard" }
  | { kind: "filter"; expr: Expr };

export type Segment = SimpleSegment | { kind: "recursive"; inner: SimpleSegment };

export interface Query {
  source: string;
  segments: Segment[];
}

export type CmpOp = "==" | "!=" | "<" | "<=" | ">" | ">=";

export type Expr =
  | { kind: "literal"; value: JsonValue }
  | { kind: "current"; path: PathSeg[] }
  | { kind: "not"; operand: Expr }
  | { kind: "and"; left: Expr; right: Expr }
  | { kind: "or"; left: Expr; right: Expr }
  | { kind: "cmp"; op: CmpOp; left: Expr; right: Expr };

export interface QueryResult {
  path: PathSeg[];
  value: JsonValue;
}

/**
 * Segments that cannot be resolved from a single forward look at the event
 * stream: filters need whole candidate values, negative indices/slices need
 * the container length. These are staged (bounded buffer, explicit spill).
 */
export function needsStaging(seg: SimpleSegment): boolean {
  switch (seg.kind) {
    case "filter":
      return true;
    case "index":
      return seg.index < 0;
    case "slice":
      return (
        (seg.start !== null && seg.start < 0) ||
        (seg.end !== null && seg.end < 0) ||
        (seg.step !== null && seg.step < 0)
      );
    default:
      return false;
  }
}
