import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { CdcLog } from "../src/cdc-log.ts";
import { RollbackEngine } from "../src/engine.ts";
import { TableStore } from "../src/store.ts";
import type { RowImage } from "../src/types.ts";

/** Deterministic millisecond clock for tests. */
function makeClock(startMs: number): { clock: () => number; set: (ms: number) => void } {
  let now = startMs;
  return { clock: () => now, set: (ms: number) => { now = ms; } };
}

function seedUsers(store: TableStore, count: number): RowImage[] {
  const users: RowImage[] = [];
  store.beginTx();
  for (let i = 1; i <= count; i++) {
    const user = {
      id: i,
      name: `user-${i}`,
      email: `user${i}@example.com`,
      phone: `138${String(i).padStart(8, "0")}`,
      balance: i * 10,
    };
    store.insert(user);
    users.push(user);
  }
  store.commitTx();
  return users;
}

describe("RollbackEngine", () => {
  it("acceptance: restores 1000 users wiped by a mistaken UPDATE", () => {
    const { clock, set } = makeClock(1_700_000_000_000);
    const log = new CdcLog();
    const users = new TableStore("users", { log, clock });
    const engine = new RollbackEngine(log);
    engine.registerStore(users);

    const original = seedUsers(users, 1000);
    assert.equal(users.size, 1000);

    // Mistaken UPDATE without WHERE: every user's profile fields are cleared.
    const beforeIncident = 1_700_000_000_500;
    set(beforeIncident);
    const badTx = users.beginTx();
    const wiped = users.updateWhere(
      () => true,
      { name: "", email: "", phone: "", balance: 0 },
    );
    users.commitTx();
    assert.equal(wiped, 1000);
    assert.equal(users.get(1)?.name, "");

    // Engine reads before images from CDC and generates 1000 reverse UPDATEs.
    const pkg = engine.buildPackageForTx(badTx, "restore wiped users");
    assert.equal(pkg.ops.length, 1000);
    assert.ok(pkg.ops.every((op) => op.op === "UPDATE"));
    assert.ok(pkg.ops.every((op) => op.sql.startsWith('UPDATE "users" SET')));

    const result = engine.replay(pkg);
    assert.equal(result.applied, 1000);

    // Millisecond-exact restoration of the pre-incident state.
    assert.deepEqual(users.allRows().sort((a, b) => (a.id as number) - (b.id as number)), original);
  });

  it("rolls back by time window, leaving earlier and later commits intact", () => {
    const { clock, set } = makeClock(1000);
    const log = new CdcLog();
    const store = new TableStore("users", { log, clock });
    const engine = new RollbackEngine(log);
    engine.registerStore(store);

    store.beginTx();
    store.insert({ id: 1, name: "keep" });
    store.commitTx();

    set(2000);
    store.beginTx();
    store.update(1, { name: "corrupted" });
    store.commitTx();

    set(3000);
    store.beginTx();
    store.update(1, { name: "also-corrupted" });
    store.commitTx();

    const pkg = engine.buildPackageForWindow(1500, 2500);
    assert.equal(pkg.ops.length, 1);
    engine.replay(pkg);
    // The 3000ms commit is logically later; window rollback restores the
    // before image of the 2000ms event only.
    assert.equal(store.get(1)?.name, "keep");
  });

  it("compensates DELETE by re-inserting the before image", () => {
    const log = new CdcLog();
    const store = new TableStore("users", { log });
    const engine = new RollbackEngine(log);
    engine.registerStore(store);

    store.beginTx();
    store.insert({ id: 1, name: "Alice", balance: 50 });
    store.commitTx();

    const badTx = store.beginTx();
    store.delete(1);
    store.commitTx();
    assert.equal(store.size, 0);

    engine.rollbackTx(badTx);
    assert.deepEqual(store.get(1), { id: 1, name: "Alice", balance: 50 });
  });

  it("compensates INSERT by deleting the row", () => {
    const log = new CdcLog();
    const store = new TableStore("users", { log });
    const engine = new RollbackEngine(log);
    engine.registerStore(store);

    const badTx = store.beginTx();
    store.insert({ id: 99, name: "intruder" });
    store.commitTx();

    engine.rollbackTx(badTx);
    assert.equal(store.get(99), undefined);
  });

  it("unwinds chained updates to the same row in reverse order", () => {
    const log = new CdcLog();
    const store = new TableStore("users", { log });
    const engine = new RollbackEngine(log);
    engine.registerStore(store);

    store.beginTx();
    store.insert({ id: 1, v: 1 });
    store.commitTx();

    const badTx = store.beginTx();
    store.update(1, { v: 2 });
    store.update(1, { v: 3 });
    store.update(1, { v: 4 });
    store.commitTx();

    engine.rollbackTx(badTx);
    assert.equal(store.get(1)?.v, 1);
  });

  it("compensation writes are themselves captured in the CDC log", () => {
    const log = new CdcLog();
    const store = new TableStore("users", { log });
    const engine = new RollbackEngine(log);
    engine.registerStore(store);

    store.beginTx();
    store.insert({ id: 1, name: "A" });
    store.commitTx();
    const sizeBefore = log.size;

    const badTx = store.beginTx();
    store.update(1, { name: "B" });
    store.commitTx();

    engine.rollbackTx(badTx);
    assert.equal(log.size, sizeBefore + 2); // bad UPDATE + compensation UPDATE
    const last = log.all().at(-1);
    assert.equal(last?.op, "UPDATE");
    assert.deepEqual(last?.before, { id: 1, name: "B" });
    assert.deepEqual(last?.after, { id: 1, name: "A" });
  });

  it("throws for unknown transactions and empty windows", () => {
    const log = new CdcLog();
    const engine = new RollbackEngine(log);
    assert.throws(() => engine.buildPackageForTx("nope"), /no CDC events/);
    assert.throws(() => engine.buildPackageForWindow(0, 1), /no CDC events/);
  });
});
