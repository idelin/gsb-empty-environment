import { inMapKey } from "./axial.ts";
import type { CellKey } from "./axial.ts";
import { ERR } from "./model.ts";
import type {
  CreateArgs,
  LeaseRec,
  Meta,
  OpKind,
  OpResult,
  ReassignArgs,
  ReleaseArgs,
  ReplaceArgs,
  SetCapacityArgs,
  SetQuotaArgs,
} from "./model.ts";

/** Committed state of the lease store. */
export interface State {
  meta: Meta | null;
  tick: number;
  capacity: Map<CellKey, number>; // overrides; absent => meta.defaultCapacity
  quota: Map<string, number>; // only finite quotas are stored
  ownership: Map<CellKey, number>; // partition overrides; absent => hash
  leases: Map<string, LeaseRec>;
  requests: Map<string, { hash: string; result: OpResult }>;
}

export function emptyState(): State {
  return {
    meta: null,
    tick: 0,
    capacity: new Map(),
    quota: new Map(),
    ownership: new Map(),
    leases: new Map(),
    requests: new Map(),
  };
}

export function cloneState(state: State): State {
  return {
    meta: state.meta,
    tick: state.tick,
    capacity: new Map(state.capacity),
    quota: new Map(state.quota),
    ownership: new Map(state.ownership),
    leases: new Map(state.leases),
    requests: new Map(state.requests),
  };
}

/**
 * A serializable description of a committed state change. Replaying an
 * effect on top of the pre-transaction state always yields the
 * post-transaction state, which is what recovery relies on.
 */
export interface Effect {
  tick?: number;
  expired?: string[];
  upsertLease?: LeaseRec;
  deleteLease?: string;
  setCapacity?: { cell: CellKey; capacity: number };
  setQuota?: { tenantId: string; quota: number | null };
  reassign?: { cell: CellKey; partition: number };
  recordRequest?: { requestId: string; hash: string; result: OpResult };
}

export function applyEffect(state: State, e: Effect): void {
  if (e.tick !== undefined) state.tick = e.tick;
  if (e.expired) for (const id of e.expired) state.leases.delete(id);
  if (e.deleteLease !== undefined) state.leases.delete(e.deleteLease);
  if (e.upsertLease) state.leases.set(e.upsertLease.leaseId, e.upsertLease);
  if (e.setCapacity) state.capacity.set(e.setCapacity.cell, e.setCapacity.capacity);
  if (e.setQuota) {
    if (e.setQuota.quota === null) state.quota.delete(e.setQuota.tenantId);
    else state.quota.set(e.setQuota.tenantId, e.setQuota.quota);
  }
  if (e.reassign) state.ownership.set(e.reassign.cell, e.reassign.partition);
  if (e.recordRequest) {
    state.requests.set(e.recordRequest.requestId, {
      hash: e.recordRequest.hash,
      result: e.recordRequest.result,
    });
  }
}

export function capacityOf(state: State, cell: CellKey): number {
  const meta = state.meta;
  if (!meta) throw new Error("store not initialized");
  return state.capacity.get(cell) ?? meta.defaultCapacity;
}

export function cellUsage(state: State): Map<CellKey, number> {
  const usage = new Map<CellKey, number>();
  for (const lease of state.leases.values()) {
    for (const cell of lease.cells) {
      usage.set(cell, (usage.get(cell) ?? 0) + 1);
    }
  }
  return usage;
}

export function tenantUsage(state: State): Map<string, number> {
  const usage = new Map<string, number>();
  for (const lease of state.leases.values()) {
    usage.set(lease.tenantId, (usage.get(lease.tenantId) ?? 0) + lease.cells.length);
  }
  return usage;
}

export function expiredAt(state: State, tick: number): string[] {
  const out: string[] = [];
  for (const lease of state.leases.values()) {
    if (lease.expiresAtTick !== null && lease.expiresAtTick <= tick) {
      out.push(lease.leaseId);
    }
  }
  return out.sort();
}

export interface Plan {
  effect: Effect;
  result: OpResult;
}

function checkCells(state: State, cells: CellKey[]): OpResult | null {
  if (!Array.isArray(cells) || cells.length === 0) {
    return { ok: false, error: ERR.INVALID_CELLS, reason: "EMPTY" };
  }
  if (new Set(cells).size !== cells.length) {
    return { ok: false, error: ERR.INVALID_CELLS, reason: "DUPLICATE" };
  }
  const radius = state.meta!.radius;
  for (const cell of cells) {
    if (typeof cell !== "string" || !inMapKey(cell, radius)) {
      return { ok: false, error: ERR.INVALID_CELLS, reason: "OUT_OF_MAP", cell };
    }
  }
  return null;
}

function checkExpiry(state: State, expiresAtTick: number | null): OpResult | null {
  if (expiresAtTick !== null) {
    if (!Number.isInteger(expiresAtTick)) {
      return { ok: false, error: ERR.BAD_ARGS, field: "expiresAtTick" };
    }
    if (expiresAtTick <= state.tick) {
      return {
        ok: false,
        error: ERR.EXPIRY_IN_PAST,
        expiresAtTick,
        tick: state.tick,
      };
    }
  }
  return null;
}

function checkNames(tenantId: unknown, leaseId: unknown): OpResult | null {
  if (typeof tenantId !== "string" || tenantId.length === 0) {
    return { ok: false, error: ERR.BAD_ARGS, field: "tenantId" };
  }
  if (typeof leaseId !== "string" || leaseId.length === 0) {
    return { ok: false, error: ERR.BAD_ARGS, field: "leaseId" };
  }
  return null;
}

/**
 * Validation order (canonical, mirrored by the reference model):
 *   1. argument shape (names, cells, expiry)
 *   2. lease existence / tenant / version
 *   3. per-cell capacity (added cells only, in given order)
 *   4. tenant quota (net change)
 */
function validate(state: State, kind: OpKind, args: unknown): OpResult {
  switch (kind) {
    case "tick":
      return { ok: true };
    case "create": {
      const c = args as CreateArgs;
      const bad =
        checkNames(c.tenantId, c.leaseId) ??
        checkCells(state, c.cells) ??
        checkExpiry(state, c.expiresAtTick);
      if (bad) return bad;
      if (state.leases.has(c.leaseId)) {
        return { ok: false, error: ERR.LEASE_EXISTS, leaseId: c.leaseId };
      }
      const usage = cellUsage(state);
      for (const cell of c.cells) {
        const cap = capacityOf(state, cell);
        const used = usage.get(cell) ?? 0;
        if (used >= cap) {
          return { ok: false, error: ERR.CELL_CAPACITY_EXCEEDED, cell, capacity: cap, usage: used };
        }
      }
      const quota = state.quota.get(c.tenantId);
      if (quota !== undefined) {
        const projected = (tenantUsage(state).get(c.tenantId) ?? 0) + c.cells.length;
        if (projected > quota) {
          return { ok: false, error: ERR.TENANT_QUOTA_EXCEEDED, tenantId: c.tenantId, quota, usage: projected };
        }
      }
      return { ok: true };
    }
    case "replace": {
      const r = args as ReplaceArgs;
      const bad =
        checkNames(r.tenantId, r.leaseId) ??
        checkCells(state, r.cells) ??
        checkExpiry(state, r.expiresAtTick);
      if (bad) return bad;
      const lease = state.leases.get(r.leaseId);
      if (!lease) return { ok: false, error: ERR.LEASE_NOT_FOUND, leaseId: r.leaseId };
      if (lease.tenantId !== r.tenantId) {
        return { ok: false, error: ERR.TENANT_MISMATCH, leaseId: r.leaseId };
      }
      if (lease.version !== r.expectedVersion) {
        return {
          ok: false,
          error: ERR.VERSION_MISMATCH,
          leaseId: r.leaseId,
          expected: r.expectedVersion,
          actual: lease.version,
        };
      }
      const old = new Set(lease.cells);
      const added = r.cells.filter((cell) => !old.has(cell));
      const usage = cellUsage(state);
      for (const cell of added) {
        const cap = capacityOf(state, cell);
        const used = usage.get(cell) ?? 0;
        if (used >= cap) {
          return { ok: false, error: ERR.CELL_CAPACITY_EXCEEDED, cell, capacity: cap, usage: used };
        }
      }
      const quota = state.quota.get(r.tenantId);
      if (quota !== undefined) {
        const projected =
          (tenantUsage(state).get(r.tenantId) ?? 0) - lease.cells.length + r.cells.length;
        if (projected > quota) {
          return { ok: false, error: ERR.TENANT_QUOTA_EXCEEDED, tenantId: r.tenantId, quota, usage: projected };
        }
      }
      return { ok: true };
    }
    case "release": {
      const r = args as ReleaseArgs;
      const bad = checkNames(r.tenantId, r.leaseId);
      if (bad) return bad;
      const lease = state.leases.get(r.leaseId);
      if (!lease) return { ok: false, error: ERR.LEASE_NOT_FOUND, leaseId: r.leaseId };
      if (lease.tenantId !== r.tenantId) {
        return { ok: false, error: ERR.TENANT_MISMATCH, leaseId: r.leaseId };
      }
      if (lease.version !== r.expectedVersion) {
        return {
          ok: false,
          error: ERR.VERSION_MISMATCH,
          leaseId: r.leaseId,
          expected: r.expectedVersion,
          actual: lease.version,
        };
      }
      return { ok: true };
    }
    case "setCapacity": {
      const s = args as SetCapacityArgs;
      if (typeof s.cell !== "string" || !inMapKey(s.cell, state.meta!.radius)) {
        return { ok: false, error: ERR.OUT_OF_MAP, cell: s.cell };
      }
      if (!Number.isInteger(s.capacity) || s.capacity < 0) {
        return { ok: false, error: ERR.BAD_ARGS, field: "capacity" };
      }
      return { ok: true };
    }
    case "setQuota": {
      const s = args as SetQuotaArgs;
      if (typeof s.tenantId !== "string" || s.tenantId.length === 0) {
        return { ok: false, error: ERR.BAD_ARGS, field: "tenantId" };
      }
      if (s.quota !== null && (!Number.isInteger(s.quota) || s.quota < 0)) {
        return { ok: false, error: ERR.BAD_ARGS, field: "quota" };
      }
      return { ok: true };
    }
    case "reassign": {
      const r = args as ReassignArgs;
      if (typeof r.cell !== "string" || !inMapKey(r.cell, state.meta!.radius)) {
        return { ok: false, error: ERR.OUT_OF_MAP, cell: r.cell };
      }
      if (
        !Number.isInteger(r.partition) ||
        r.partition < 0 ||
        r.partition >= state.meta!.numPartitions
      ) {
        return { ok: false, error: ERR.BAD_ARGS, field: "partition" };
      }
      return { ok: true };
    }
  }
}

/**
 * Plan a transaction without mutating `state`. Caller guarantees the op
 * is not an idempotent replay and tick >= state.tick. A tick advance
 * (with its expiry sweep) is folded into the same effect, so expirations
 * are always processed before the request at that tick.
 */
export function planOp(
  state: State,
  kind: OpKind,
  args: unknown,
  tick: number,
  hash: string | null,
  requestId: string | null,
): Plan {
  const effect: Effect = {};
  let working = state;
  let expired: string[] = [];
  if (tick > state.tick) {
    expired = expiredAt(state, tick);
    effect.tick = tick;
    if (expired.length > 0) effect.expired = expired;
    working = cloneState(state);
    working.tick = tick;
    for (const id of expired) working.leases.delete(id);
  }

  let result: OpResult;
  if (kind === "tick") {
    result = { ok: true, tick, expired };
  } else {
    result = validate(working, kind, args);
    if (result.ok) {
      switch (kind) {
        case "create": {
          const c = args as CreateArgs;
          const cells = [...c.cells].sort();
          effect.upsertLease = {
            leaseId: c.leaseId,
            tenantId: c.tenantId,
            cells,
            version: 1,
            expiresAtTick: c.expiresAtTick,
          };
          result = { ok: true, leaseId: c.leaseId, version: 1 };
          break;
        }
        case "replace": {
          const r = args as ReplaceArgs;
          const lease = working.leases.get(r.leaseId)!;
          const cells = [...r.cells].sort();
          effect.upsertLease = {
            leaseId: lease.leaseId,
            tenantId: lease.tenantId,
            cells,
            version: lease.version + 1,
            expiresAtTick: r.expiresAtTick,
          };
          result = { ok: true, leaseId: lease.leaseId, version: lease.version + 1 };
          break;
        }
        case "release": {
          const r = args as ReleaseArgs;
          effect.deleteLease = r.leaseId;
          result = { ok: true, leaseId: r.leaseId };
          break;
        }
        case "setCapacity": {
          const s = args as SetCapacityArgs;
          effect.setCapacity = { cell: s.cell, capacity: s.capacity };
          result = { ok: true, cell: s.cell, capacity: s.capacity };
          break;
        }
        case "setQuota": {
          const s = args as SetQuotaArgs;
          effect.setQuota = { tenantId: s.tenantId, quota: s.quota };
          result = { ok: true, tenantId: s.tenantId, quota: s.quota };
          break;
        }
        case "reassign": {
          const r = args as ReassignArgs;
          effect.reassign = { cell: r.cell, partition: r.partition };
          result = { ok: true, cell: r.cell, partition: r.partition };
          break;
        }
      }
    }
  }

  if (requestId !== null && hash !== null) {
    effect.recordRequest = { requestId, hash, result };
  }
  return { effect, result };
}
