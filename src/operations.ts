import type { VectorClock } from "./vectorClock.ts";

export type OpKind = "set" | "delete";

export interface Operation {
  id: string;
  replicaId: string;
  txnId: string;
  txnSize: number;
  clock: VectorClock;
  kind: OpKind;
  field: string;
  value?: unknown;
  ts: number;
}
