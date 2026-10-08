export type OpStatus = "pending" | "sent" | "acked" | "failed" | "blocked";

export interface Operation {
  id: string;
  kind: string;
  /** temp ids this operation brings into existence (e.g. a create). */
  provides?: string[];
  /** explicit dependencies on other operation ids. */
  dependsOn?: string[];
  /** arbitrary JSON; any string equal to a temp id is a reference. */
  payload?: unknown;
}

export type Receipt =
  | { opId: string; ok: true; idMap?: Record<string, string> }
  | { opId: string; ok: false; error?: string };

export interface Transport {
  send(op: Operation): Promise<Receipt>;
}

export interface OpRecord {
  op: Operation;
  status: OpStatus;
  error?: string;
}

export interface PersistedState {
  version: 1;
  ops: OpRecord[];
  idMap: Record<string, string>;
}

export interface FlushSummary {
  acked: string[];
  failed: string[];
  blocked: string[];
  pending: string[];
}
