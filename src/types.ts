export type State = Record<string, unknown>;

export type Unsubscribe = () => void;

export type Op =
  | { kind: 'set'; path: string; value: unknown }
  | { kind: 'delete'; path: string };

export type JournalEntry =
  | { type: 'tx'; id: number; at: string; ops: Op[] }
  | {
      type: 'replace';
      at: string;
      reason: string;
      fromVersion: string;
      toVersion: string;
      state: State;
    };

export interface RenameMap {
  [oldPath: string]: string;
}

export interface Migration {
  from: string;
  to: string;
  up: (state: State) => State;
  renames?: RenameMap;
}

export type AuditKind =
  | 'migrate-step'
  | 'migrate-commit'
  | 'migrate-failed'
  | 'migrate-rejected';

export interface AuditEntry {
  seq: number;
  at: string;
  kind: AuditKind;
  fromVersion: string;
  toVersion: string;
  renames: RenameMap;
  detail?: string;
}
