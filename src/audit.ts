import type { AuditEntry, AuditKind, RenameMap } from './types.ts';
import { nowIso } from './util.ts';

export class AuditLog {
  #entries: AuditEntry[] = [];

  record(
    kind: AuditKind,
    info: {
      fromVersion: string;
      toVersion: string;
      renames?: RenameMap;
      detail?: string;
    },
  ): AuditEntry {
    const entry: AuditEntry = {
      seq: this.#entries.length + 1,
      at: nowIso(),
      kind,
      fromVersion: info.fromVersion,
      toVersion: info.toVersion,
      renames: info.renames ?? {},
      ...(info.detail !== undefined ? { detail: info.detail } : {}),
    };
    this.#entries.push(entry);
    return entry;
  }

  entries(): readonly AuditEntry[] {
    return this.#entries;
  }
}
