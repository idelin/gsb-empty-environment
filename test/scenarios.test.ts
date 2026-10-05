import { existsSync, mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import assert from "node:assert/strict";
import { allCells } from "../src/axial.ts";
import { auditStore } from "../src/audit.ts";
import { computeLedger } from "../src/ledger.ts";
import { CrashFault, LeaseStore, partitionLogPath } from "../src/store.ts";
import { ERR } from "../src/model.ts";
import type { Hooks } from "../src/store.ts";

const META = { radius: 3, defaultCapacity: 1, numPartitions: 4 };

function initStore(hooks: Hooks = {}) {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-scen-"));
  LeaseStore.init(dir, META);
  return { dir, store: LeaseStore.open(dir, hooks) };
}

test("cross-partition migration is atomically visible to queries", () => {
  // Find cells owned by three distinct partitions.
  const probe = initStore();
  const byPartition = new Map<number, string[]>();
  for (const cell of allCells(META.radius)) {
    const p = probe.store.partitionOf(cell);
    byPartition.set(p, [...(byPartition.get(p) ?? []), cell]);
  }
  probe.store.close();

  const midTxObservations: { cells: string[] | undefined; version: number | undefined }[] = [];
  const hooks: Hooks = {
    afterPartitionWrite: (store) => {
      // Queries issued while the transaction is between partition writes
      // must still see the pre-transaction committed state.
      const lease = store.getLease("L1");
      midTxObservations.push({ cells: lease?.cells, version: lease?.version });
    },
  };
  const { dir, store } = initStore(hooks);

  const [pA, pB, pC] = [...byPartition.keys()];
  const home = byPartition.get(pA)![0];
  const targetB = byPartition.get(pB)![0];
  const targetC = byPartition.get(pC)![0];
  assert.notEqual(store.partitionOf(home), store.partitionOf(targetB));
  assert.notEqual(store.partitionOf(targetB), store.partitionOf(targetC));

  store.create({ tenantId: "t1", leaseId: "L1", cells: [home], expiresAtTick: null }, 0);
  midTxObservations.length = 0;
  const r = store.replace(
    {
      tenantId: "t1",
      leaseId: "L1",
      expectedVersion: 1,
      cells: [targetB, targetC],
      expiresAtTick: null,
      requestId: "mig-1",
    },
    0,
  );
  assert.equal(r.ok, true);
  assert.ok(midTxObservations.length >= 2, "transaction spanned multiple partitions");
  for (const obs of midTxObservations) {
    assert.deepEqual(obs, { cells: [home], version: 1 }, "mid-transaction query saw uncommitted state");
  }
  // After commit the whole new region is visible at once.
  assert.deepEqual(store.getLease("L1")!.cells, [targetB, targetC].sort());
  const ledger = computeLedger(store.view());
  assert.equal(ledger.cells.find((c) => c.cell === home)!.usage, 0);
  assert.equal(ledger.cells.find((c) => c.cell === targetB)!.usage, 1);
  assert.equal(ledger.cells.find((c) => c.cell === targetC)!.usage, 1);

  const report = auditStore(dir);
  assert.ok(report.ok, JSON.stringify(report.partitionErrors));
  store.close();
});

test("partition ownership can be reassigned; replace still commits atomically", () => {
  const { dir, store } = initStore();
  const cell = allCells(META.radius)[0];
  const target = (store.partitionOf(cell) + 1) % META.numPartitions;
  const r = store.reassign({ cell, partition: target }, 0);
  assert.equal(r.ok, true);
  assert.equal(store.partitionOf(cell), target);

  store.create({ tenantId: "t1", leaseId: "L1", cells: [cell], expiresAtTick: null }, 0);
  const other = allCells(META.radius).find(
    (c) => store.partitionOf(c) !== target,
  )!;
  const moved = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: [cell, other], expiresAtTick: null },
    0,
  );
  assert.equal(moved.ok, true);
  assert.deepEqual(store.getLease("L1")!.cells, [cell, other].sort());
  // Ownership survives restart and the audit agrees with the new owner.
  store.close();
  const reopened = LeaseStore.open(dir);
  assert.equal(reopened.partitionOf(cell), target);
  reopened.close();
  const report = auditStore(dir);
  assert.deepEqual(report.partitionErrors, []);
  assert.ok(report.ok);
});

test("overlapping old/new sets: net change is checked, not the union", () => {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-scen-"));
  LeaseStore.init(dir, { radius: 1, defaultCapacity: 1, numPartitions: 2 });
  const store = LeaseStore.open(dir);
  // Fill the whole map except one cell with two leases.
  const cells = allCells(1); // 7 cells
  const shared = cells.slice(0, 3);
  const l1Only = cells[3];
  const free = cells[4];
  store.setQuota({ tenantId: "t1", quota: 10 }, 0);
  store.create({ tenantId: "t1", leaseId: "L1", cells: [...shared, l1Only], expiresAtTick: null }, 0);
  // Replacing onto the retained cells must not count them twice.
  const r = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: [...shared, free], expiresAtTick: null },
    0,
  );
  assert.equal(r.ok, true);
  assert.deepEqual(store.getLease("L1")!.cells, [...shared, free].sort());
  // The released cell is free again; the retained ones are still full.
  const ledger = computeLedger(store.view());
  assert.equal(ledger.cells.find((c) => c.cell === l1Only)!.usage, 0);
  for (const cell of shared) {
    assert.equal(ledger.cells.find((c) => c.cell === cell)!.usage, 1);
  }
  store.close();
  assert.ok(auditStore(dir).ok);
});

test("capacity reduction: existing usage grandfathered, new grants blocked", () => {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-scen-"));
  LeaseStore.init(dir, { radius: 1, defaultCapacity: 2, numPartitions: 2 });
  const store = LeaseStore.open(dir);
  const cell = "0,0";
  store.create({ tenantId: "t1", leaseId: "L1", cells: [cell], expiresAtTick: null }, 0);
  store.create({ tenantId: "t2", leaseId: "L2", cells: [cell], expiresAtTick: null }, 0);
  // Reduce capacity below current usage: allowed, but nothing new may be granted.
  const reduced = store.setCapacity({ cell, capacity: 1 }, 0);
  assert.equal(reduced.ok, true);
  let ledger = computeLedger(store.view());
  const entry = ledger.cells.find((c) => c.cell === cell)!;
  assert.equal(entry.usage, 2);
  assert.equal(entry.overCapacity, true);
  const blocked = store.create(
    { tenantId: "t3", leaseId: "L3", cells: [cell], expiresAtTick: null },
    0,
  );
  assert.equal(blocked.error, ERR.CELL_CAPACITY_EXCEEDED);
  // A replace that keeps the cell is still allowed (no new grant on it).
  const kept = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: [cell, "1,0"], expiresAtTick: null },
    0,
  );
  assert.equal(kept.ok, true);
  // Releasing one lease brings the cell back within capacity.
  store.release({ tenantId: "t2", leaseId: "L2", expectedVersion: 1 }, 0);
  ledger = computeLedger(store.view());
  assert.equal(ledger.cells.find((c) => c.cell === cell)!.overCapacity, false);
  store.close();

  const report = auditStore(dir);
  assert.deepEqual(report.grantViolations, [], "no grant ever exceeded capacity");
  assert.ok(report.ok);
});

test("partial partition write failure aborts the whole transaction", () => {
  const fault = { armed: true, writes: 0 };
  const hooks: Hooks = {
    beforePartitionWrite: () => {
      fault.writes++;
      if (fault.armed && fault.writes === 2) throw new Error("disk full on partition");
    },
  };
  const { dir, store } = initStore(hooks);
  const cells = allCells(META.radius);
  const spanning = [...new Set(cells.map((c) => store.partitionOf(c)))].slice(0, 3);
  const region = spanning.map((p) => cells.find((c) => store.partitionOf(c) === p)!);
  store.create({ tenantId: "t1", leaseId: "L1", cells: [region[0]], expiresAtTick: null }, 0);
  fault.writes = 0; // count only this transaction's partition writes
  const before = store.dump();

  const r = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: region, expiresAtTick: null, requestId: "tx-9" },
    0,
  );
  assert.equal(r.error, ERR.TX_ABORTED);
  assert.deepEqual(store.dump(), before, "aborted transaction must leave no trace");
  assert.equal(store.getLease("L1")!.version, 1);

  // The request id was not consumed: the client may retry as-is.
  fault.armed = false;
  const retry = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: region, expiresAtTick: null, requestId: "tx-9" },
    0,
  );
  assert.equal(retry.ok, true);
  assert.deepEqual(store.getLease("L1")!.cells, [...region].sort());
  store.close();

  const report = auditStore(dir);
  assert.equal(report.abortedTxs, 1);
  assert.deepEqual(report.partitionErrors, []);
  assert.ok(report.ok);
});

test("crash before commit: pending intent is never treated as released capacity", () => {
  const fault = { armed: false };
  const hooks: Hooks = {
    beforeCommit: () => {
      if (fault.armed) throw new CrashFault();
    },
  };
  const { dir, store } = initStore(hooks);
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0", "1,0"], expiresAtTick: null }, 0);
  fault.armed = true;
  assert.throws(
    () =>
      store.replace(
        { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: ["2,0"], expiresAtTick: null, requestId: "tx-crash" },
        0,
      ),
    CrashFault,
  );

  // Recover in a fresh process image.
  const recovered = LeaseStore.open(dir);
  assert.equal(recovered.pendingIntents().length, 1);
  // The old region is still fully held: the pending transaction did not
  // release it, and its new region was never granted.
  assert.deepEqual(recovered.getLease("L1")!.cells, ["0,0", "1,0"]);
  assert.equal(recovered.getLease("L1")!.version, 1);
  const ledger = computeLedger(recovered.view());
  assert.equal(ledger.cells.find((c) => c.cell === "0,0")!.usage, 1);
  assert.equal(ledger.cells.find((c) => c.cell === "2,0")!.usage, 0);
  // A competing tenant still cannot take the old cells.
  const blocked = recovered.create(
    { tenantId: "t2", leaseId: "L2", cells: ["0,0"], expiresAtTick: null },
    0,
  );
  assert.equal(blocked.error, ERR.CELL_CAPACITY_EXCEEDED);
  // The crashed request id is reusable: the transaction never happened.
  const retried = recovered.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: ["2,0"], expiresAtTick: null, requestId: "tx-crash" },
    0,
  );
  assert.equal(retried.ok, true);
  recovered.close();

  const report = auditStore(dir);
  assert.equal(report.orphanIntents, 1);
  assert.ok(report.ok);
});

test("crash between partition writes leaves no partial visibility", () => {
  const fault = { writes: 0 };
  const hooks: Hooks = {
    beforePartitionWrite: () => {
      fault.writes++;
      if (fault.writes === 2) throw new CrashFault("crash mid-transaction");
    },
  };
  const { dir, store } = initStore(hooks);
  const cells = allCells(META.radius);
  const partitions = [...new Set(cells.map((c) => store.partitionOf(c)))].slice(0, 3);
  const region = partitions.map((p) => cells.find((c) => store.partitionOf(c) === p)!);
  store.create({ tenantId: "t1", leaseId: "L1", cells: [region[0]], expiresAtTick: null }, 0);
  fault.writes = 0; // crash on the 2nd partition write of the replace
  assert.throws(
    () =>
      store.replace(
        { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: region, expiresAtTick: null },
        0,
      ),
    CrashFault,
  );
  const recovered = LeaseStore.open(dir);
  assert.deepEqual(recovered.getLease("L1")!.cells, [region[0]]);
  assert.equal(recovered.getLease("L1")!.version, 1);
  // Orphaned partition-log records exist but belong to no committed tx.
  const partLogs = partitions
    .map((p) => partitionLogPath(dir, p))
    .filter((p) => existsSync(p))
    .map((p) => readFileSync(p, "utf8").trim().split("\n").map((l) => JSON.parse(l)))
    .flat();
  const orphans = partLogs.filter((r) => r.type === "effect");
  assert.ok(orphans.length >= 1, "partial writes were persisted before the crash");
  recovered.close();
  const report = auditStore(dir);
  assert.ok(report.ok);
  assert.equal(report.orphanIntents, 1);
});

test("idempotency and conflict survive restart", () => {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-scen-"));
  LeaseStore.init(dir, META);
  let store = LeaseStore.open(dir);
  const args = {
    tenantId: "t1",
    leaseId: "L1",
    cells: ["0,0"],
    expiresAtTick: null as number | null,
    requestId: "persistent-req",
  };
  const first = store.create(args, 0);
  store.close();
  store = LeaseStore.open(dir);
  assert.deepEqual(store.create(args, 0), first, "replay after restart");
  const conflict = store.create({ ...args, cells: ["1,1"] }, 0);
  assert.equal(conflict.error, ERR.REQUEST_CONFLICT);
  store.close();
});
