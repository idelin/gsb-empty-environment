import { stampEquals, type VectorEntry } from "./types.ts";

/**
 * An immutable snapshot of the stamps of every file a diagnostic result
 * depends on (the target file plus its transitive dependencies).
 *
 * A diagnostic computed against vector V may only be shown or reused while
 * the current state of EVERY file in V still equals V. Matching only the
 * target file's version is explicitly not sufficient.
 */
export class VersionVector {
  readonly entries: ReadonlyMap<string, VectorEntry>;

  constructor(entries: ReadonlyMap<string, VectorEntry>) {
    this.entries = new Map(entries);
  }

  equals(other: VersionVector): boolean {
    if (this.entries.size !== other.entries.size) {
      return false;
    }
    for (const [uri, entry] of this.entries) {
      if (!other.entries.has(uri)) {
        return false;
      }
      if (!stampEquals(entry, other.entries.get(uri) ?? null)) {
        return false;
      }
    }
    return true;
  }

  toString(): string {
    const parts: string[] = [];
    for (const [uri, entry] of [...this.entries].sort()) {
      parts.push(entry === null ? `${uri}:<absent>` : `${uri}:${entry.epoch}.${entry.version}`);
    }
    return `{${parts.join(", ")}}`;
  }
}
