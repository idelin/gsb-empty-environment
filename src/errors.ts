import type { Seg } from "./path.ts";

export type ErrorKind =
  | "MalformedRecord"
  | "DuplicateDefinition"
  | "MissingParent"
  | "MissingRoot"
  | "ParentTypeConflict"
  | "SegmentTypeMismatch"
  | "IndexOutOfRange"
  | "UnexpectedDelete"
  | "NotFound"
  | "TargetExists"
  | "MoveIntoSelf"
  | "NoVersion";

/**
 * Every failure is pinned to a concrete path and, when known, to the origin
 * of the offending record (e.g. "run-0003.jsonl:17" or "input#42").
 */
export class PathError extends Error {
  readonly kind: ErrorKind;
  readonly path: Seg[];
  readonly source: string | undefined;

  constructor(kind: ErrorKind, path: Seg[], message: string, source?: string) {
    super(source !== undefined ? `${message} [source: ${source}]` : message);
    this.name = "PathError";
    this.kind = kind;
    this.path = path;
    this.source = source;
  }
}
