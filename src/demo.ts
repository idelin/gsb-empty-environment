/**
 * Runnable sample:
 *  1. Build the 5-transaction crash scenario (checkpoint + partial flush).
 *  2. Run ARIES three-phase recovery and print each phase's decisions.
 *  3. Demo the multi-tenant DRR scheduler with weighted fairness + quotas.
 */
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AriesRecovery } from "./recovery.ts";
import { buildCrashScenario } from "./scenario.ts";
import { TenantScheduler } from "./scheduler.ts";
import { DiskManager } from "./storage.ts";
import { WalManager } from "./wal.ts";

function recoveryDemo(): void {
  console.log("=== ARIES crash recovery demo ===");
  const dir = mkdtempSync(join(tmpdir(), "aries-demo-"));
  const scenario = buildCrashScenario(dir);
  console.log(`WAL file:   ${scenario.walPath}`);
  console.log(`Disk image: ${scenario.diskPath}`);
  console.log("--- WAL at crash time ---");
  for (const r of WalManager.open(scenario.walPath).all()) {
    const extra =
      r.type === "CHECKPOINT"
        ? ""
        : ` txn=${r.txnId} prev=${r.prevLSN}` +
          (r.pageId ? ` page=${r.pageId} '${r.beforeImage}'->'${r.afterImage}'` : "");
    console.log(`  LSN ${String(r.lsn).padStart(2)}  ${r.type.padEnd(10)}${extra}`);
  }
  console.log("--- Disk at crash time ---");
  for (const p of DiskManager.open(scenario.diskPath).allPages()) {
    console.log(`  ${p.pageId}: '${p.data}' (pageLSN=${p.pageLSN})`);
  }

  const wal = WalManager.open(scenario.walPath);
  const disk = DiskManager.open(scenario.diskPath);
  const recovery = new AriesRecovery(wal, disk);

  console.log("--- Phase 1: Analysis ---");
  const analysis = recovery.analysis();
  for (const e of analysis.transactionTable.values()) {
    console.log(`  T${e.txnId}: ${e.status} lastLSN=${e.lastLSN}`);
  }
  for (const e of analysis.dirtyPageTable.values()) {
    console.log(`  DPT ${e.pageId}: recLSN=${e.recLSN}`);
  }
  console.log(`  redoStartLSN=${analysis.redoStartLSN}`);

  console.log("--- Phase 2: Redo (repeat history) ---");
  const redone = recovery.redo(analysis);
  console.log(`  redone LSNs: [${redone.join(", ")}]`);

  console.log("--- Phase 3: Undo (losers, with CLRs) ---");
  const undo = recovery.undo(analysis);
  console.log(`  undone update LSNs: [${undo.undoneLSNs.join(", ")}]`);
  console.log(`  CLR LSNs written:   [${undo.clrLSNs.join(", ")}]`);

  console.log("--- Disk after recovery ---");
  for (const p of disk.allPages()) {
    const expected = scenario.expectedAfterRecovery[p.pageId];
    const ok = p.data === expected ? "OK" : `MISMATCH (expected '${expected}')`;
    console.log(`  ${p.pageId}: '${p.data}'  ${ok}`);
  }
}

async function schedulerDemo(): Promise<void> {
  console.log("=== Multi-tenant DRR scheduler demo ===");
  const scheduler = new TenantScheduler(100);
  scheduler.registerTenant("heavy", {
    weight: 3, quotaPerWindow: 1000, windowMs: 60000, maxConcurrency: 4, maxQueue: 1000,
  });
  scheduler.registerTenant("light", {
    weight: 1, quotaPerWindow: 1000, windowMs: 60000, maxConcurrency: 4, maxQueue: 1000,
  });

  const completionOrder: string[] = [];
  for (let i = 0; i < 12; i++) {
    scheduler.submit({
      id: `heavy-${i}`, tenantId: "heavy", cost: 100,
      run: async () => { completionOrder.push("heavy"); },
    });
    scheduler.submit({
      id: `light-${i}`, tenantId: "light", cost: 100,
      run: async () => { completionOrder.push("light"); },
    });
  }
  await scheduler.drain();

  console.log("  stats heavy:", scheduler.tenantStats("heavy"));
  console.log("  stats light:", scheduler.tenantStats("light"));
  console.log(`  first 12 completions: ${completionOrder.slice(0, 12).join(", ")}`);
  console.log("  (weight 3:1 -> roughly 3 heavy tasks served per light task)");
}

recoveryDemo();
await schedulerDemo();
