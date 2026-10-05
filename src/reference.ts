import { inMapKey } from "./axial.ts";
import type { CellKey } from "./axial.ts";
import { ERR, cloneResult, hashPayload } from "./model.ts";
import type {
  CreateArgs,
  LeaseRec,
  OpResult,
  ReleaseArgs,
  ReplaceArgs,
  SetCapacityArgs,
  SetQuotaArgs,
} from "./model.ts";

/**
 * Serial single-table reference model. No partitions, no journal, no
 * two-phase commit: every operation is validated and applied directly
 * against one in-memory table, in call order. The differential tests run
 * identical workloads against this model and the journaled LeaseStore and
 * require identical results and identical ledgers.
 */
export class ReferenceStore {
  private radius: number;
  private defaultCapacity: number;
  private clock = 0;
  private capacity = new Map<CellKey, number>();
  private quota = new Map<string, number>();
  private leases = new Map<string, LeaseRec>();
  private requests = new Map<string, { hash: string; result: OpResult }>();

  constructor(radius: number, defaultCapacity: number) {
    this.radius = radius;
    this.defaultCapacity = defaultCapacity;
  }

  get currentTick(): number {
    return this.clock;
  }

  private cap(cell: CellKey): number {
    return this.capacity.get(cell) ?? this.defaultCapacity;
  }

  private sweep(tick: number): string[] {
    const expired: string[] = [];
    for (const lease of this.leases.values()) {
      if (lease.expiresAtTick !== null && lease.expiresAtTick <= tick) {
        expired.push(lease.leaseId);
      }
    }
    expired.sort();
    for (const id of expired) this.leases.delete(id);
    return expired;
  }

  private cellUse(cell: CellKey): number {
    let n = 0;
    for (const lease of this.leases.values()) {
      if (lease.cells.includes(cell)) n++;
    }
    return n;
  }

  private tenantUse(tenantId: string): number {
    let n = 0;
    for (const lease of this.leases.values()) {
      if (lease.tenantId === tenantId) n += lease.cells.length;
    }
    return n;
  }

  /** Idempotent replay / conflict, shared by all request-id-bearing ops. */
  private replay(
    kind: string,
    args: unknown,
    requestId: string | undefined,
  ): OpResult | null {
    if (requestId === undefined) return null;
    const seen = this.requests.get(requestId);
    if (!seen) return null;
    if (hashPayload(kind as never, args) === seen.hash) return cloneResult(seen.result);
    return { ok: false, error: ERR.REQUEST_CONFLICT, requestId };
  }

  private remember(requestId: string | undefined, kind: string, args: unknown, result: OpResult): void {
    if (requestId === undefined) return;
    this.requests.set(requestId, { hash: hashPayload(kind as never, args), result });
  }

  private advance(tick: number): OpResult | null {
    if (!Number.isInteger(tick) || tick < this.clock) {
      return { ok: false, error: ERR.TICK_REGRESSION, tick, currentTick: this.clock };
    }
    if (tick > this.clock) {
      this.sweep(tick);
      this.clock = tick;
    }
    return null;
  }

  tick(t: number): OpResult {
    if (!Number.isInteger(t) || t < this.clock) {
      return { ok: false, error: ERR.TICK_REGRESSION, tick: t, currentTick: this.clock };
    }
    const expired = t > this.clock ? this.sweep(t) : [];
    this.clock = t;
    return { ok: true, tick: t, expired };
  }

  private checkCells(cells: CellKey[]): OpResult | null {
    if (!Array.isArray(cells) || cells.length === 0) {
      return { ok: false, error: ERR.INVALID_CELLS, reason: "EMPTY" };
    }
    if (new Set(cells).size !== cells.length) {
      return { ok: false, error: ERR.INVALID_CELLS, reason: "DUPLICATE" };
    }
    for (const cell of cells) {
      if (typeof cell !== "string" || !inMapKey(cell, this.radius)) {
        return { ok: false, error: ERR.INVALID_CELLS, reason: "OUT_OF_MAP", cell };
      }
    }
    return null;
  }

  private checkExpiry(expiresAtTick: number | null): OpResult | null {
    if (expiresAtTick !== null) {
      if (!Number.isInteger(expiresAtTick)) {
        return { ok: false, error: ERR.BAD_ARGS, field: "expiresAtTick" };
      }
      if (expiresAtTick <= this.clock) {
        return { ok: false, error: ERR.EXPIRY_IN_PAST, expiresAtTick, tick: this.clock };
      }
    }
    return null;
  }

  private checkNames(tenantId: unknown, leaseId: unknown): OpResult | null {
    if (typeof tenantId !== "string" || tenantId.length === 0) {
      return { ok: false, error: ERR.BAD_ARGS, field: "tenantId" };
    }
    if (typeof leaseId !== "string" || leaseId.length === 0) {
      return { ok: false, error: ERR.BAD_ARGS, field: "leaseId" };
    }
    return null;
  }

  create(args: CreateArgs, tick: number): OpResult {
    const seen = this.replay("create", args, args.requestId);
    if (seen) return seen;
    const advanced = this.advance(tick);
    if (advanced) return advanced;
    const result = this.doCreate(args);
    this.remember(args.requestId, "create", args, result);
    return result;
  }

  private doCreate(args: CreateArgs): OpResult {
    const bad =
      this.checkNames(args.tenantId, args.leaseId) ??
      this.checkCells(args.cells) ??
      this.checkExpiry(args.expiresAtTick);
    if (bad) return bad;
    if (this.leases.has(args.leaseId)) {
      return { ok: false, error: ERR.LEASE_EXISTS, leaseId: args.leaseId };
    }
    for (const cell of args.cells) {
      const usage = this.cellUse(cell);
      const capacity = this.cap(cell);
      if (usage >= capacity) {
        return { ok: false, error: ERR.CELL_CAPACITY_EXCEEDED, cell, capacity, usage };
      }
    }
    const quota = this.quota.get(args.tenantId);
    if (quota !== undefined) {
      const usage = this.tenantUse(args.tenantId) + args.cells.length;
      if (usage > quota) {
        return { ok: false, error: ERR.TENANT_QUOTA_EXCEEDED, tenantId: args.tenantId, quota, usage };
      }
    }
    const lease: LeaseRec = {
      leaseId: args.leaseId,
      tenantId: args.tenantId,
      cells: [...args.cells].sort(),
      version: 1,
      expiresAtTick: args.expiresAtTick,
    };
    this.leases.set(lease.leaseId, lease);
    return { ok: true, leaseId: lease.leaseId, version: 1 };
  }

  replace(args: ReplaceArgs, tick: number): OpResult {
    const seen = this.replay("replace", args, args.requestId);
    if (seen) return seen;
    const advanced = this.advance(tick);
    if (advanced) return advanced;
    const result = this.doReplace(args);
    this.remember(args.requestId, "replace", args, result);
    return result;
  }

  private doReplace(args: ReplaceArgs): OpResult {
    const bad =
      this.checkNames(args.tenantId, args.leaseId) ??
      this.checkCells(args.cells) ??
      this.checkExpiry(args.expiresAtTick);
    if (bad) return bad;
    const lease = this.leases.get(args.leaseId);
    if (!lease) return { ok: false, error: ERR.LEASE_NOT_FOUND, leaseId: args.leaseId };
    if (lease.tenantId !== args.tenantId) {
      return { ok: false, error: ERR.TENANT_MISMATCH, leaseId: args.leaseId };
    }
    if (lease.version !== args.expectedVersion) {
      return {
        ok: false,
        error: ERR.VERSION_MISMATCH,
        leaseId: args.leaseId,
        expected: args.expectedVersion,
        actual: lease.version,
      };
    }
    const old = new Set(lease.cells);
    const added = args.cells.filter((cell) => !old.has(cell));
    for (const cell of added) {
      const usage = this.cellUse(cell);
      const capacity = this.cap(cell);
      if (usage >= capacity) {
        return { ok: false, error: ERR.CELL_CAPACITY_EXCEEDED, cell, capacity, usage };
      }
    }
    const quota = this.quota.get(args.tenantId);
    if (quota !== undefined) {
      const usage = this.tenantUse(args.tenantId) - lease.cells.length + args.cells.length;
      if (usage > quota) {
        return { ok: false, error: ERR.TENANT_QUOTA_EXCEEDED, tenantId: args.tenantId, quota, usage };
      }
    }
    lease.cells = [...args.cells].sort();
    lease.version += 1;
    lease.expiresAtTick = args.expiresAtTick;
    return { ok: true, leaseId: lease.leaseId, version: lease.version };
  }

  release(args: ReleaseArgs, tick: number): OpResult {
    const seen = this.replay("release", args, args.requestId);
    if (seen) return seen;
    const advanced = this.advance(tick);
    if (advanced) return advanced;
    const result = this.doRelease(args);
    this.remember(args.requestId, "release", args, result);
    return result;
  }

  private doRelease(args: ReleaseArgs): OpResult {
    const bad = this.checkNames(args.tenantId, args.leaseId);
    if (bad) return bad;
    const lease = this.leases.get(args.leaseId);
    if (!lease) return { ok: false, error: ERR.LEASE_NOT_FOUND, leaseId: args.leaseId };
    if (lease.tenantId !== args.tenantId) {
      return { ok: false, error: ERR.TENANT_MISMATCH, leaseId: args.leaseId };
    }
    if (lease.version !== args.expectedVersion) {
      return {
        ok: false,
        error: ERR.VERSION_MISMATCH,
        leaseId: args.leaseId,
        expected: args.expectedVersion,
        actual: lease.version,
      };
    }
    this.leases.delete(args.leaseId);
    return { ok: true, leaseId: args.leaseId };
  }

  setCapacity(args: SetCapacityArgs, tick: number): OpResult {
    const advanced = this.advance(tick);
    if (advanced) return advanced;
    if (typeof args.cell !== "string" || !inMapKey(args.cell, this.radius)) {
      return { ok: false, error: ERR.OUT_OF_MAP, cell: args.cell };
    }
    if (!Number.isInteger(args.capacity) || args.capacity < 0) {
      return { ok: false, error: ERR.BAD_ARGS, field: "capacity" };
    }
    this.capacity.set(args.cell, args.capacity);
    return { ok: true, cell: args.cell, capacity: args.capacity };
  }

  setQuota(args: SetQuotaArgs, tick: number): OpResult {
    const advanced = this.advance(tick);
    if (advanced) return advanced;
    if (typeof args.tenantId !== "string" || args.tenantId.length === 0) {
      return { ok: false, error: ERR.BAD_ARGS, field: "tenantId" };
    }
    if (args.quota !== null && (!Number.isInteger(args.quota) || args.quota < 0)) {
      return { ok: false, error: ERR.BAD_ARGS, field: "quota" };
    }
    if (args.quota === null) this.quota.delete(args.tenantId);
    else this.quota.set(args.tenantId, args.quota);
    return { ok: true, tenantId: args.tenantId, quota: args.quota };
  }

  getLease(leaseId: string): LeaseRec | undefined {
    const lease = this.leases.get(leaseId);
    return lease ? { ...lease, cells: [...lease.cells] } : undefined;
  }

  /** Same shape as LeaseStore.dump() (minus partition ownership). */
  dump(): Record<string, unknown> {
    const sortEntries = (m: Map<string, number>) =>
      [...m.entries()].sort(([a], [b]) => (a < b ? -1 : 1));
    return {
      tick: this.clock,
      leases: [...this.leases.values()]
        .map((l) => ({ ...l, cells: [...l.cells] }))
        .sort((a, b) => (a.leaseId < b.leaseId ? -1 : 1)),
      capacity: sortEntries(this.capacity),
      quota: sortEntries(this.quota),
      requests: [...this.requests.keys()].sort(),
    };
  }
}
