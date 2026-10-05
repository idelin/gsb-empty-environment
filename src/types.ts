// Shared types and canonical request hashing (used for idempotency).

export interface Lease {
  id: string;
  tenant: string;
  cells: string[]; // sorted, unique cell keys ("q,r")
  version: number; // starts at 1, +1 per successful replace
  expiresAt: number; // lease expires when tick >= expiresAt
}

export type ErrorCode =
  | "MAP_ALREADY_INITIALIZED"
  | "MAP_NOT_INITIALIZED"
  | "TICK_REGRESSION"
  | "REQUEST_CONFLICT"
  | "TX_ABORTED"
  | "LEASE_EXISTS"
  | "LEASE_NOT_FOUND"
  | "VERSION_CONFLICT"
  | "UNKNOWN_TENANT"
  | "CELL_OUT_OF_MAP"
  | "CELL_CAPACITY_EXCEEDED"
  | "TENANT_QUOTA_EXCEEDED"
  | "CAPACITY_BELOW_OCCUPANCY"
  | "INVALID_CAPACITY"
  | "INVALID_EXPIRY"
  | "UNKNOWN_PARTITION";

export type OpResult = { ok: true; [k: string]: unknown } | { ok: false; error: ErrorCode };

export interface CreateRequest {
  requestId: string;
  tick: number;
  tenant: string;
  leaseId: string;
  cells: string[];
  expiresAt: number;
}

export interface ReplaceRequest {
  requestId: string;
  tick: number;
  leaseId: string;
  expectedVersion: number;
  cells: string[];
  expiresAt?: number | null; // null/undefined keeps the current expiry
}

export type IntentOp =
  | { kind: "create"; tick: number; tenant: string; leaseId: string; cells: string[]; expiresAt: number }
  | { kind: "replace"; tick: number; leaseId: string; expectedVersion: number; cells: string[]; expiresAt: number | null };

export type JournalRecord =
  | { type: "map"; radius: number; capacity: number; partitions: number }
  | { type: "quota"; tenant: string; quota: number }
  | { type: "capacity"; cell: string; capacity: number }
  | { type: "tick"; tick: number }
  | { type: "reassign"; partition: number; owner: string }
  | { type: "intent"; requestId: string; op: IntentOp }
  | { type: "resolution"; requestId: string; outcome: "commit" | "abort"; result: OpResult };

export function normalizeCells(cells: string[]): string[] {
  return [...new Set(cells)].sort();
}

export function createHash(req: CreateRequest): string {
  return JSON.stringify(["create", req.tick, req.tenant, req.leaseId, normalizeCells(req.cells), req.expiresAt]);
}

export function replaceHash(req: ReplaceRequest): string {
  return JSON.stringify([
    "replace",
    req.tick,
    req.leaseId,
    req.expectedVersion,
    normalizeCells(req.cells),
    req.expiresAt ?? null,
  ]);
}

export function intentHash(op: IntentOp): string {
  return op.kind === "create"
    ? JSON.stringify(["create", op.tick, op.tenant, op.leaseId, op.cells, op.expiresAt])
    : JSON.stringify(["replace", op.tick, op.leaseId, op.expectedVersion, op.cells, op.expiresAt]);
}
