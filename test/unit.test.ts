import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import assert from "node:assert/strict";
import { LeaseStore } from "../src/store.ts";
import { computeLedger } from "../src/ledger.ts";
import { ERR } from "../src/model.ts";

function freshStore(meta = { radius: 2, defaultCapacity: 1, numPartitions: 3 }) {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-unit-"));
  LeaseStore.init(dir, meta);
  return LeaseStore.open(dir);
}

test("create and query a lease", () => {
  const store = freshStore();
  const r = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["0,0", "1,0"], expiresAtTick: null, requestId: "r1" },
    0,
  );
  assert.deepEqual(r, { ok: true, leaseId: "L1", version: 1 });
  const lease = store.getLease("L1");
  assert.deepEqual(lease, {
    leaseId: "L1",
    tenantId: "t1",
    cells: ["0,0", "1,0"],
    version: 1,
    expiresAtTick: null,
  });
  store.close();
});

test("create validation: duplicates, empty, out-of-map, existing id", () => {
  const store = freshStore();
  assert.equal(
    store.create({ tenantId: "t1", leaseId: "L1", cells: [], expiresAtTick: null }, 0).error,
    ERR.INVALID_CELLS,
  );
  const dup = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["0,0", "0,0"], expiresAtTick: null },
    0,
  );
  assert.deepEqual([dup.error, dup.reason], [ERR.INVALID_CELLS, "DUPLICATE"]);
  const out = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["9,9"], expiresAtTick: null },
    0,
  );
  assert.deepEqual([out.error, out.reason], [ERR.INVALID_CELLS, "OUT_OF_MAP"]);
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: null }, 0);
  assert.equal(
    store.create({ tenantId: "t1", leaseId: "L1", cells: ["1,0"], expiresAtTick: null }, 0).error,
    ERR.LEASE_EXISTS,
  );
  store.close();
});

test("per-cell capacity is enforced", () => {
  const store = freshStore();
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: null }, 0);
  const r = store.create(
    { tenantId: "t2", leaseId: "L2", cells: ["0,0"], expiresAtTick: null },
    0,
  );
  assert.equal(r.error, ERR.CELL_CAPACITY_EXCEEDED);
  assert.deepEqual([r.cell, r.capacity, r.usage], ["0,0", 1, 1]);
  store.close();
});

test("tenant quota counts total cells across leases", () => {
  const store = freshStore();
  store.setQuota({ tenantId: "t1", quota: 2 }, 0);
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0", "1,0"], expiresAtTick: null }, 0);
  const r = store.create(
    { tenantId: "t1", leaseId: "L2", cells: ["2,0"], expiresAtTick: null },
    0,
  );
  assert.equal(r.error, ERR.TENANT_QUOTA_EXCEEDED);
  assert.deepEqual([r.quota, r.usage], [2, 3]);
  store.close();
});

test("replace never releases before the new set is fully validated", () => {
  const store = freshStore();
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: null }, 0);
  store.create({ tenantId: "t2", leaseId: "L2", cells: ["1,0"], expiresAtTick: null }, 0);
  // Moving onto a full cell must fail and leave the old region intact.
  const fail = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: ["1,0"], expiresAtTick: null },
    0,
  );
  assert.equal(fail.error, ERR.CELL_CAPACITY_EXCEEDED);
  assert.deepEqual(store.getLease("L1")!.cells, ["0,0"]);
  assert.equal(store.getLease("L1")!.version, 1);
  // Growing onto a full cell must also fail atomically.
  const fail2 = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: ["0,0", "1,0"], expiresAtTick: null },
    0,
  );
  assert.equal(fail2.error, ERR.CELL_CAPACITY_EXCEEDED);
  assert.deepEqual(store.getLease("L1")!.cells, ["0,0"]);
  // Once the cell is free the same replace succeeds as one transaction.
  store.release({ tenantId: "t2", leaseId: "L2", expectedVersion: 1 }, 0);
  const ok = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: ["0,0", "1,0"], expiresAtTick: null },
    0,
  );
  assert.deepEqual(ok, { ok: true, leaseId: "L1", version: 2 });
  assert.deepEqual(store.getLease("L1")!.cells, ["0,0", "1,0"]);
  store.close();
});

test("replace with overlapping old/new sets only checks the delta", () => {
  const store = freshStore();
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0", "1,0"], expiresAtTick: null }, 0);
  // capacity is 1 everywhere; keeping 0,0 while swapping 1,0 -> -1,0 must work
  const r = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 1, cells: ["0,0", "-1,0"], expiresAtTick: null },
    0,
  );
  assert.deepEqual(r, { ok: true, leaseId: "L1", version: 2 });
  assert.deepEqual(store.getLease("L1")!.cells, ["-1,0", "0,0"]);
  store.close();
});

test("replace enforces tenant and version", () => {
  const store = freshStore();
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: null }, 0);
  const wrongTenant = store.replace(
    { tenantId: "t2", leaseId: "L1", expectedVersion: 1, cells: ["1,0"], expiresAtTick: null },
    0,
  );
  assert.equal(wrongTenant.error, ERR.TENANT_MISMATCH);
  const wrongVersion = store.replace(
    { tenantId: "t1", leaseId: "L1", expectedVersion: 7, cells: ["1,0"], expiresAtTick: null },
    0,
  );
  assert.equal(wrongVersion.error, ERR.VERSION_MISMATCH);
  assert.deepEqual([wrongVersion.expected, wrongVersion.actual], [7, 1]);
  const missing = store.replace(
    { tenantId: "t1", leaseId: "nope", expectedVersion: 1, cells: ["1,0"], expiresAtTick: null },
    0,
  );
  assert.equal(missing.error, ERR.LEASE_NOT_FOUND);
  assert.deepEqual(store.getLease("L1")!.cells, ["0,0"]);
  store.close();
});

test("expiry is processed before same-tick requests", () => {
  const store = freshStore();
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: 5 }, 0);
  store.tick(4);
  assert.ok(store.getLease("L1"), "alive before expiry tick");
  // A request arriving at tick 5 must see L1 already expired: capacity freed.
  const r = store.create(
    { tenantId: "t2", leaseId: "L2", cells: ["0,0"], expiresAtTick: null, requestId: "r2" },
    5,
  );
  assert.deepEqual(r, { ok: true, leaseId: "L2", version: 1 });
  assert.equal(store.getLease("L1"), undefined);
  store.close();
});

test("expiry in the past is rejected", () => {
  const store = freshStore();
  store.tick(3);
  const r = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: 3 },
    3,
  );
  assert.equal(r.error, ERR.EXPIRY_IN_PAST);
  store.close();
});

test("tick regression is rejected", () => {
  const store = freshStore();
  store.tick(5);
  const r = store.tick(4);
  assert.equal(r.error, ERR.TICK_REGRESSION);
  const c = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: null },
    4,
  );
  assert.equal(c.error, ERR.TICK_REGRESSION);
  store.close();
});

test("idempotent replay and conflicting content", () => {
  const store = freshStore();
  const args = {
    tenantId: "t1",
    leaseId: "L1",
    cells: ["0,0"],
    expiresAtTick: null as number | null,
    requestId: "req-1",
  };
  const first = store.create(args, 0);
  const replay = store.create(args, 0);
  assert.deepEqual(replay, first);
  assert.equal(store.dump().leases.length, 1, "replay must not double-apply");
  const conflict = store.create({ ...args, cells: ["1,0"] }, 0);
  assert.equal(conflict.error, ERR.REQUEST_CONFLICT);
  // set-equal payload in different order is the same content
  const args2 = { ...args, leaseId: "L2", requestId: "req-2", cells: ["1,0", "0,1"] };
  store.create(args2, 0);
  const reordered = store.create({ ...args2, cells: ["0,1", "1,0"] }, 0);
  assert.equal(reordered.ok, true);
  store.close();
});

test("release frees capacity and quota", () => {
  const store = freshStore();
  store.setQuota({ tenantId: "t1", quota: 1 }, 0);
  store.create({ tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: null }, 0);
  store.release({ tenantId: "t1", leaseId: "L1", expectedVersion: 1 }, 0);
  assert.equal(store.getLease("L1"), undefined);
  const again = store.create(
    { tenantId: "t1", leaseId: "L2", cells: ["0,0"], expiresAtTick: null },
    0,
  );
  assert.equal(again.ok, true);
  const ledger = computeLedger(store.view());
  const t1 = ledger.tenants.find((t) => t.tenantId === "t1")!;
  assert.equal(t1.usage, 1);
  store.close();
});

test("state survives a clean restart", () => {
  const dir = mkdtempSync(join(tmpdir(), "hexlease-unit-"));
  LeaseStore.init(dir, { radius: 2, defaultCapacity: 1, numPartitions: 3 });
  let store = LeaseStore.open(dir);
  store.setQuota({ tenantId: "t1", quota: 3 }, 0);
  store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: 9, requestId: "r1" },
    2,
  );
  store.close();

  store = LeaseStore.open(dir);
  assert.equal(store.currentTick, 2);
  assert.deepEqual(store.getLease("L1")!.cells, ["0,0"]);
  // idempotency survives restart
  const replay = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["0,0"], expiresAtTick: 9, requestId: "r1" },
    2,
  );
  assert.deepEqual(replay, { ok: true, leaseId: "L1", version: 1 });
  const conflict = store.create(
    { tenantId: "t1", leaseId: "L1", cells: ["1,0"], expiresAtTick: 9, requestId: "r1" },
    2,
  );
  assert.equal(conflict.error, ERR.REQUEST_CONFLICT);
  store.close();
});
