import { allCells } from "./axial.ts";
import type { CellKey } from "./axial.ts";
import type { LeaseRec } from "./model.ts";

/** Minimal committed-state view needed to compute a ledger. */
export interface LedgerView {
  meta: { radius: number; defaultCapacity: number } | null;
  leases: Map<string, LeaseRec>;
  capacity: Map<CellKey, number>;
  quota: Map<string, number>;
}

export interface CellLedgerEntry {
  cell: CellKey;
  capacity: number;
  usage: number;
  leases: string[];
  /** usage > capacity: only reachable via capacity reduction (grandfathered). */
  overCapacity: boolean;
}

export interface TenantLedgerEntry {
  tenantId: string;
  quota: number | null;
  usage: number;
  leases: string[];
  overQuota: boolean;
}

export interface Ledger {
  tick?: number;
  cells: CellLedgerEntry[];
  tenants: TenantLedgerEntry[];
}

export function computeLedger(view: LedgerView): Ledger {
  if (!view.meta) throw new Error("store not initialized");
  const cellLeases = new Map<CellKey, string[]>();
  const tenantLeases = new Map<string, string[]>();
  for (const lease of view.leases.values()) {
    const list = tenantLeases.get(lease.tenantId) ?? [];
    list.push(lease.leaseId);
    tenantLeases.set(lease.tenantId, list);
    for (const cell of lease.cells) {
      const owners = cellLeases.get(cell) ?? [];
      owners.push(lease.leaseId);
      cellLeases.set(cell, owners);
    }
  }

  const cells: CellLedgerEntry[] = allCells(view.meta.radius).map((cell) => {
    const leases = (cellLeases.get(cell) ?? []).slice().sort();
    const capacity = view.capacity.get(cell) ?? view.meta!.defaultCapacity;
    return {
      cell,
      capacity,
      usage: leases.length,
      leases,
      overCapacity: leases.length > capacity,
    };
  });

  const tenantIds = new Set<string>([...view.quota.keys(), ...tenantLeases.keys()]);
  const tenants: TenantLedgerEntry[] = [...tenantIds].sort().map((tenantId) => {
    const leases = (tenantLeases.get(tenantId) ?? []).slice().sort();
    const usage = leases.reduce(
      (sum, id) => sum + (view.leases.get(id)?.cells.length ?? 0),
      0,
    );
    const quota = view.quota.get(tenantId) ?? null;
    return {
      tenantId,
      quota,
      usage,
      leases,
      overQuota: quota !== null && usage > quota,
    };
  });

  return { cells, tenants };
}

/**
 * Snapshot-level violations. An over-capacity cell here is only possible
 * when capacity was reduced below existing usage; grant-time over-allocation
 * is proven separately by the journal audit (see audit.ts).
 */
export function ledgerViolations(ledger: Ledger): string[] {
  const out: string[] = [];
  for (const cell of ledger.cells) {
    if (cell.overCapacity) {
      out.push(
        `cell ${cell.cell}: usage ${cell.usage} > capacity ${cell.capacity}`,
      );
    }
  }
  for (const tenant of ledger.tenants) {
    if (tenant.overQuota) {
      out.push(
        `tenant ${tenant.tenantId}: usage ${tenant.usage} > quota ${tenant.quota}`,
      );
    }
  }
  return out;
}
