import type { CellKey } from "./axial.ts";

/** A lease: tenant + cell set + version + expiry tick. */
export interface LeaseRec {
  leaseId: string;
  tenantId: string;
  cells: CellKey[]; // sorted, unique
  version: number;
  expiresAtTick: number | null; // null = never expires
}

export interface Meta {
  radius: number;
  defaultCapacity: number;
  numPartitions: number;
}

export type OpKind =
  | "tick"
  | "create"
  | "replace"
  | "release"
  | "setCapacity"
  | "setQuota"
  | "reassign";

export interface CreateArgs {
  tenantId: string;
  leaseId: string;
  cells: CellKey[];
  expiresAtTick: number | null;
  requestId?: string;
}

export interface ReplaceArgs {
  tenantId: string;
  leaseId: string;
  expectedVersion: number;
  cells: CellKey[];
  expiresAtTick: number | null;
  requestId?: string;
}

export interface ReleaseArgs {
  tenantId: string;
  leaseId: string;
  expectedVersion: number;
  requestId?: string;
}

export interface SetCapacityArgs {
  cell: CellKey;
  capacity: number;
}

export interface SetQuotaArgs {
  tenantId: string;
  quota: number | null; // null = unlimited
}

export interface ReassignArgs {
  cell: CellKey;
  partition: number;
}

export type OpResult =
  | ({ ok: true } & Record<string, unknown>)
  | ({ ok: false; error: string } & Record<string, unknown>);

export const ERR = {
  TICK_REGRESSION: "TICK_REGRESSION",
  REQUEST_CONFLICT: "REQUEST_CONFLICT",
  INVALID_CELLS: "INVALID_CELLS",
  LEASE_EXISTS: "LEASE_EXISTS",
  LEASE_NOT_FOUND: "LEASE_NOT_FOUND",
  TENANT_MISMATCH: "TENANT_MISMATCH",
  VERSION_MISMATCH: "VERSION_MISMATCH",
  CELL_CAPACITY_EXCEEDED: "CELL_CAPACITY_EXCEEDED",
  TENANT_QUOTA_EXCEEDED: "TENANT_QUOTA_EXCEEDED",
  EXPIRY_IN_PAST: "EXPIRY_IN_PAST",
  OUT_OF_MAP: "OUT_OF_MAP",
  TX_ABORTED: "TX_ABORTED",
  BAD_ARGS: "BAD_ARGS",
} as const;

/** Deterministic JSON with sorted object keys (arrays keep order). */
export function canonical(value: unknown): string {
  if (Array.isArray(value)) {
    return "[" + value.map(canonical).join(",") + "]";
  }
  if (value !== null && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
    return (
      "{" +
      entries.map(([k, v]) => JSON.stringify(k) + ":" + canonical(v)).join(",") +
      "}"
    );
  }
  return JSON.stringify(value ?? null);
}

/**
 * Content hash of a request payload, used for idempotency. The `cells`
 * array is normalized (sorted, deduplicated) so that set-equal payloads
 * are treated as the same content; any other difference conflicts.
 */
export function hashPayload(kind: OpKind, args: unknown): string {
  let normalized = args;
  if (args !== null && typeof args === "object") {
    const rec = args as Record<string, unknown>;
    if (Array.isArray(rec.cells)) {
      normalized = { ...rec, cells: [...new Set(rec.cells as CellKey[])].sort() };
    }
  }
  return canonical({ kind, args: normalized });
}

export function cloneResult(result: OpResult): OpResult {
  return JSON.parse(JSON.stringify(result)) as OpResult;
}
