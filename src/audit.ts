import { join } from "node:path";
import { Ledger } from "./ledger.ts";
import type { Allocation, Grant } from "./types.ts";

export interface WindowReport {
  windowId: number;
  quota: number;
  issued: number;
  returned: number;
  consumed: number;
  idle: number;
  ok: boolean;
}

export interface RejectionRecord {
  nodeId: string;
  requestId: string;
  reason: string;
  tick: number;
}

export interface ConflictRecord {
  nodeId: string;
  requestId: string;
  existingCost: number;
  attemptedCost: number;
  tick: number;
}

export interface AuditReport {
  windows: WindowReport[];
  conflicts: ConflictRecord[];
  rejections: RejectionRecord[];
  violations: string[];
  totalAccepted: number;
  totalConsumed: number;
}

/**
 * Independent verifier. Reads ONLY the persisted ledgers (never live
 * state), replays every accepted consume exactly once per request id,
 * and checks the safety invariants:
 *   - center spent(w) = issued(w) - confirmedReturned(w) <= quota at every event
 *   - per-window total consumption <= quota and <= issued
 *   - no consume drew on an expired grant
 *   - duplicate request ids are counted once; cost conflicts are reported
 */
export function auditLedgers(opts: {
  dir: string;
  nodeIds: string[];
  windowQuota: number;
  windowLength: number;
}): AuditReport {
  const violations: string[] = [];
  const conflicts: ConflictRecord[] = [];
  const rejections: RejectionRecord[] = [];

  // ---- Center ledger: grants, returns, spent invariant ----
  const centerEvents = Ledger.read(join(opts.dir, "center.ledger.jsonl"));
  const grants = new Map<string, Grant>();
  const spentByWindow = new Map<number, number>();
  const issuedByWindow = new Map<number, number>();
  const returnedByWindow = new Map<number, number>();
  const confirmedReturns = new Set<string>();

  const bump = (map: Map<number, number>, key: number, delta: number) =>
    map.set(key, (map.get(key) ?? 0) + delta);

  for (const event of centerEvents) {
    if (event.ev === "grant_issued") {
      const grant = event.grant as Grant;
      if (grants.has(grant.grantId)) {
        violations.push(`duplicate grant id ${grant.grantId} issued by center`);
        continue;
      }
      grants.set(grant.grantId, grant);
      bump(spentByWindow, grant.windowId, grant.amount);
      bump(issuedByWindow, grant.windowId, grant.amount);
      const spent = spentByWindow.get(grant.windowId)!;
      if (spent > opts.windowQuota) {
        violations.push(
          `window ${grant.windowId}: center spent ${spent} exceeds quota ${opts.windowQuota} after issuing ${grant.grantId}`,
        );
      }
      if (grant.expiryTick !== (grant.windowId + 1) * opts.windowLength - 1) {
        violations.push(`grant ${grant.grantId}: expiry tick does not match its window`);
      }
    } else if (event.ev === "return_confirmed") {
      const returnId = event.returnId as string;
      if (confirmedReturns.has(returnId)) {
        violations.push(`return ${returnId} confirmed twice by center`);
        continue;
      }
      confirmedReturns.add(returnId);
      const grant = grants.get(event.grantId as string);
      const amount = event.acceptedAmount as number;
      if (!grant) {
        violations.push(`return ${returnId} references unknown grant ${event.grantId}`);
        continue;
      }
      bump(spentByWindow, grant.windowId, -amount);
      bump(returnedByWindow, grant.windowId, amount);
      if (spentByWindow.get(grant.windowId)! < 0) {
        violations.push(`window ${grant.windowId}: center spent went negative after return ${returnId}`);
      }
    } else if (event.ev === "borrow_rejected") {
      rejections.push({
        nodeId: event.nodeId as string,
        requestId: event.requestId as string,
        reason: event.reason as string,
        tick: event.tick,
      });
    }
  }

  // ---- Node ledgers: accepted consumes, deduped by request id ----
  const acceptedById = new Map<string, { nodeId: string; cost: number; tick: number; allocations: Allocation[] }>();
  const consumedByWindow = new Map<number, number>();

  for (const nodeId of opts.nodeIds) {
    const events = Ledger.read(join(opts.dir, `node-${nodeId}.ledger.jsonl`));
    for (const event of events) {
      if (event.ev === "consume_accepted") {
        const key = `${nodeId}/${event.requestId}`;
        const cost = event.cost as number;
        const tick = event.tick;
        const allocations = event.allocations as Allocation[];
        const existing = acceptedById.get(key);
        if (existing) {
          if (existing.cost !== cost) {
            violations.push(`request ${key} accepted twice with different costs`);
          }
          continue; // same id counted once
        }
        const sum = allocations.reduce((acc, a) => acc + a.amount, 0);
        if (sum !== cost) {
          violations.push(`request ${key}: allocations sum ${sum} != cost ${cost}`);
        }
        for (const alloc of allocations) {
          const grant = grants.get(alloc.grantId);
          if (!grant) {
            violations.push(`request ${key}: drew on unknown grant ${alloc.grantId}`);
          } else {
            if (grant.nodeId !== nodeId) {
              violations.push(`request ${key}: drew on grant ${alloc.grantId} of node ${grant.nodeId}`);
            }
            if (tick > grant.expiryTick) {
              violations.push(
                `request ${key}: consumed at tick ${tick} after grant ${alloc.grantId} expired at ${grant.expiryTick}`,
              );
            }
          }
        }
        acceptedById.set(key, { nodeId, cost, tick, allocations });
        bump(consumedByWindow, Math.floor(tick / opts.windowLength), cost);
      } else if (event.ev === "consume_rejected") {
        rejections.push({
          nodeId,
          requestId: event.requestId as string,
          reason: event.reason as string,
          tick: event.tick,
        });
      } else if (event.ev === "consume_conflict") {
        conflicts.push({
          nodeId,
          requestId: event.requestId as string,
          existingCost: event.existingCost as number,
          attemptedCost: event.cost as number,
          tick: event.tick,
        });
      }
    }
  }

  // ---- Per-window rollup ----
  const windowIds = new Set<number>([
    ...issuedByWindow.keys(),
    ...consumedByWindow.keys(),
    ...returnedByWindow.keys(),
  ]);
  const windows: WindowReport[] = [...windowIds].sort((a, b) => a - b).map((windowId) => {
    const issued = issuedByWindow.get(windowId) ?? 0;
    const returned = returnedByWindow.get(windowId) ?? 0;
    const consumed = consumedByWindow.get(windowId) ?? 0;
    const idle = opts.windowQuota - consumed;
    let ok = true;
    if (consumed > opts.windowQuota) {
      violations.push(`window ${windowId}: consumed ${consumed} exceeds quota ${opts.windowQuota}`);
      ok = false;
    }
    if (consumed > issued) {
      violations.push(`window ${windowId}: consumed ${consumed} exceeds issued ${issued}`);
      ok = false;
    }
    if (issued - returned > opts.windowQuota) {
      violations.push(`window ${windowId}: outstanding ${issued - returned} exceeds quota`);
      ok = false;
    }
    return { windowId, quota: opts.windowQuota, issued, returned, consumed, idle, ok };
  });

  let totalConsumed = 0;
  for (const w of windows) totalConsumed += w.consumed;

  return {
    windows,
    conflicts,
    rejections,
    violations,
    totalAccepted: acceptedById.size,
    totalConsumed,
  };
}

export function formatReport(report: AuditReport): string {
  const lines: string[] = [];
  lines.push("window | quota | issued | returned | consumed | idle | ok");
  lines.push("-------+-------+--------+----------+----------+------+---");
  for (const w of report.windows) {
    lines.push(
      `${String(w.windowId).padStart(6)} | ${String(w.quota).padStart(5)} | ${String(w.issued).padStart(6)} | ${String(w.returned).padStart(8)} | ${String(w.consumed).padStart(8)} | ${String(w.idle).padStart(4)} | ${w.ok ? "yes" : "NO"}`,
    );
  }
  lines.push(`accepted requests (deduped): ${report.totalAccepted}`);
  lines.push(`total consumed: ${report.totalConsumed}`);
  lines.push(`rejections: ${report.rejections.length}`);
  const byReason = new Map<string, number>();
  for (const r of report.rejections) byReason.set(r.reason, (byReason.get(r.reason) ?? 0) + 1);
  for (const [reason, count] of [...byReason].sort()) {
    lines.push(`  - ${reason}: ${count}`);
  }
  lines.push(`cost conflicts (same request id, different cost): ${report.conflicts.length}`);
  for (const c of report.conflicts.slice(0, 10)) {
    lines.push(
      `  - ${c.nodeId}/${c.requestId}: settled cost ${c.existingCost}, rejected attempt ${c.attemptedCost} @tick ${c.tick}`,
    );
  }
  if (report.violations.length === 0) {
    lines.push("violations: none");
  } else {
    lines.push(`violations: ${report.violations.length}`);
    for (const v of report.violations) lines.push(`  ! ${v}`);
  }
  return lines.join("\n");
}
