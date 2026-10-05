import type { Uri } from "./types.ts";

/**
 * A stamp uniquely identifying one concrete state of a file's content.
 *
 * `version` is monotonic within a single open session but resets when the
 * file is closed and reopened, so it is never sufficient on its own.
 * `contentHash` identifies the actual bytes, which is what makes stamps
 * comparable across close/reopen cycles and undo operations.
 */
export interface FileStamp {
  version: number;
  contentHash: string;
}

/**
 * A version vector captures the exact state of every file a diagnostic
 * computation depended on: the primary file plus every dependency read
 * during the computation. A `null` stamp records "the file did not exist
 * (was not open) at compute time".
 */
export type VersionVector = ReadonlyMap<Uri, FileStamp | null>;

export function hashContent(content: string): string {
  // FNV-1a 32-bit, hex encoded. Synchronous and dependency-free; the hash
  // only needs to distinguish content states, not be cryptographic.
  let hash = 0x811c9dc5;
  for (let i = 0; i < content.length; i++) {
    hash ^= content.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, "0");
}

export function stampsEqual(a: FileStamp, b: FileStamp): boolean {
  return a.version === b.version && a.contentHash === b.contentHash;
}

export interface VersionVectorEnvironment {
  /** Current stamp of an open file, or `undefined` when not open/missing. */
  currentStamp(uri: Uri): FileStamp | undefined;
}

/**
 * A vector matches the environment iff every recorded dependency is in
 * exactly the recorded state:
 *  - stamp `null`  -> the file must still be absent, and
 *  - stamp `s`     -> the file must be open with a stamp equal to `s`.
 *
 * Equality of the primary file's version alone is NOT sufficient: any
 * dependency drifting (edited, closed, deleted, or newly appeared)
 * invalidates the vector.
 */
export function vectorMatches(
  vector: VersionVector,
  env: VersionVectorEnvironment,
): boolean {
  for (const [uri, stamp] of vector) {
    const current = env.currentStamp(uri);
    if (stamp === null) {
      if (current !== undefined) return false;
    } else {
      if (current === undefined || !stampsEqual(stamp, current)) return false;
    }
  }
  return true;
}
