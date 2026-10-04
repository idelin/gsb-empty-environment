export interface Position {
  readonly line: number;
  readonly character: number;
}

export interface Range {
  readonly start: Position;
  readonly end: Position;
}

export type DiagnosticSeverity = "error" | "warning" | "information" | "hint";

export interface Diagnostic {
  readonly range: Range;
  readonly message: string;
  readonly severity?: DiagnosticSeverity;
  readonly source?: string;
}

/**
 * A file's identity at a point in time.
 *
 * `version` alone is NOT a reliable identity: editors restart version
 * numbering when a file is closed and reopened, and a deleted file may be
 * recreated later with the same versions. `epoch` is a per-file generation
 * counter that increments on every (re)open, so (epoch, version) pairs are
 * unique for the lifetime of the manager.
 */
export interface FileStamp {
  readonly epoch: number;
  readonly version: number;
}

/**
 * Entry in a version vector. `null` means "the file was absent (closed,
 * deleted, or never registered) when the snapshot was taken". Recording
 * absence explicitly makes "dependency deleted" and "dependency appeared"
 * both detectable as mismatches.
 */
export type VectorEntry = FileStamp | null;

export function stampEquals(a: VectorEntry, b: VectorEntry): boolean {
  if (a === null || b === null) {
    return a === b;
  }
  return a.epoch === b.epoch && a.version === b.version;
}
