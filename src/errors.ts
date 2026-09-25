import { formatPath, type Path } from "./json.ts";

export type MergeSide = "base" | "ours" | "theirs";

export type MergeErrorCode = "MISSING_ID" | "INVALID_ID" | "DUPLICATE_ID";

export class MergeError extends Error {
  readonly code: MergeErrorCode;
  readonly side: MergeSide;
  readonly path: Path;

  constructor(code: MergeErrorCode, side: MergeSide, path: Path, detail: string) {
    super(`${detail} (at ${formatPath(path)}, ${side} side)`);
    this.name = "MergeError";
    this.code = code;
    this.side = side;
    this.path = path;
  }
}
