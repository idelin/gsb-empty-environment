import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { ReplayEngine } from "../src/engine.ts";
import { FileJournal, MemoryJournal } from "../src/journal.ts";
import type { Operation } from "../src/types.ts";
import { FakeServer } from "./fake-server.ts";

function op(id: string, extra: Partial<Operation> = {}): Operation {
  return { id, kind: "edit", ...extra };
}

test("topological order and recursive temp-id rewrite", async () => {
  const server = new FakeServer();
  server.plan("create-1", { type: "success", idMap: { "temp:1": "srv:100" } });
  server.plan("create-2", { type: "success", idMap: { "temp:2": "srv:200" } });
  const engine = await ReplayEngine.open(new MemoryJournal(), server);

  await engine.enqueue(op("use-both", {
    kind: "relate",
    payload: { from: "temp:1", to: "temp:2" },
  }));
  await engine.enqueue(op("update-1", {
    kind: "update",
    payload: {
      id: "temp:1",
      patch: { links: ["temp:1", "temp:2"], meta: { parent: "temp:2" } },
    },
  }));
  await engine.enqueue(op("create-1", { kind: "create", provides: ["temp:1"] }));
  await engine.enqueue(op("create-2", { kind: "create", provides: ["temp:2"] }));

  const summary = await engine.flush();

  assert.deepEqual(summary.failed, []);
  assert.deepEqual(summary.blocked, []);
  assert.deepEqual(summary.acked.slice().sort(), ["create-1", "create-2", "update-1", "use-both"]);

  const sentOrder = server.received.map((received) => received.id);
  assert.ok(sentOrder.indexOf("create-1") < sentOrder.indexOf("update-1"));
  assert.ok(sentOrder.indexOf("create-2") < sentOrder.indexOf("update-1"));
  assert.ok(sentOrder.indexOf("create-1") < sentOrder.indexOf("use-both"));
  assert.ok(sentOrder.indexOf("create-2") < sentOrder.indexOf("use-both"));

  const updatePayload = server.received.find((received) => received.id === "update-1")?.payload;
  assert.deepEqual(updatePayload, {
    id: "srv:100",
    patch: { links: ["srv:100", "srv:200"], meta: { parent: "srv:200" } },
  });
  const relatePayload = server.received.find((received) => received.id === "use-both")?.payload;
  assert.deepEqual(relatePayload, { from: "srv:100", to: "srv:200" });
});

test("partial failure blocks only transitive dependents", async () => {
  const server = new FakeServer();
  server.plan("create-a", { type: "success", idMap: { "temp:a": "srv:a" } });
  server.plan("create-b", { type: "failure", error: "validation" });
  const engine = await ReplayEngine.open(new MemoryJournal(), server);

  await engine.enqueue(op("create-a", { kind: "create", provides: ["temp:a"] }));
  await engine.enqueue(op("create-b", { kind: "create", provides: ["temp:b"] }));
  await engine.enqueue(op("update-a", { payload: { id: "temp:a", name: "ok" } }));
  await engine.enqueue(op("update-b", { payload: { id: "temp:b", name: "doomed" } }));
  await engine.enqueue(op("update-b-child", { payload: { parent: "temp:b", id: "temp:a" } }));
  await engine.enqueue(op("delete-x", { kind: "delete", payload: { id: "srv:9" } }));

  const summary = await engine.flush();

  assert.deepEqual(summary.failed, ["create-b"]);
  assert.deepEqual(summary.blocked.sort(), ["update-b", "update-b-child"]);
  assert.deepEqual(summary.pending, []);
  assert.deepEqual(summary.acked.sort(), ["create-a", "delete-x", "update-a"]);
  assert.equal(engine.errorOf("create-b"), "validation");

  const sentIds = server.received.map((received) => received.id);
  assert.ok(!sentIds.includes("update-b"));
  assert.ok(!sentIds.includes("update-b-child"));
  assert.ok(sentIds.includes("delete-x"));

  const updateA = server.received.find((received) => received.id === "update-a");
  assert.deepEqual(updateA?.payload, { id: "srv:a", name: "ok" });
});

test("explicit dependsOn failure also cascades", async () => {
  const server = new FakeServer();
  server.plan("step-1", { type: "failure", error: "boom" });
  const engine = await ReplayEngine.open(new MemoryJournal(), server);

  await engine.enqueue(op("step-1"));
  await engine.enqueue(op("step-2", { dependsOn: ["step-1"] }));
  await engine.enqueue(op("step-3", { dependsOn: ["step-2"] }));
  await engine.enqueue(op("parallel"));

  const summary = await engine.flush();

  assert.deepEqual(summary.failed, ["step-1"]);
  assert.deepEqual(summary.blocked, ["step-2", "step-3"]);
  assert.deepEqual(summary.acked, ["parallel"]);
});

test("duplicate receipts are idempotent no-ops", async () => {
  const server = new FakeServer();
  server.plan("create-1", { type: "success", idMap: { "temp:1": "srv:1" } });
  const engine = await ReplayEngine.open(new MemoryJournal(), server);

  await engine.enqueue(op("create-1", { kind: "create", provides: ["temp:1"] }));
  await engine.enqueue(op("update-1", { payload: { id: "temp:1" } }));
  await engine.flush();
  assert.equal(engine.statusOf("create-1"), "acked");

  await engine.applyReceipt({ opId: "create-1", ok: true, idMap: { "temp:1": "srv:OTHER" } });
  assert.equal(engine.resolvedId("temp:1"), "srv:1");
  assert.equal(engine.statusOf("create-1"), "acked");

  await engine.applyReceipt({ opId: "create-1", ok: false, error: "late failure" });
  assert.equal(engine.statusOf("create-1"), "acked");

  await engine.applyReceipt({ opId: "ghost", ok: true });
  assert.equal(engine.statusOf("ghost"), undefined);
});

test("restart recovery: lost receipt is retransmitted, server dedupes", async () => {
  const dir = await mkdtemp(join(tmpdir(), "replay-"));
  try {
    const journalPath = join(dir, "journal.json");
    const server = new FakeServer();
    server.plan("create-1", { type: "drop", idMap: { "temp:1": "srv:1" } });

    const crashed = await ReplayEngine.open(new FileJournal(journalPath), server);
    await crashed.enqueue(op("create-1", { kind: "create", provides: ["temp:1"] }));
    await crashed.enqueue(op("update-1", { payload: { id: "temp:1", v: 2 } }));
    await crashed.enqueue(op("independent", { payload: { id: "srv:0" } }));

    await assert.rejects(crashed.flush(), /connection lost/);
    assert.equal(crashed.statusOf("create-1"), "sent");
    assert.equal(server.commitCount("create-1"), 1);

    const recovered = await ReplayEngine.open(new FileJournal(journalPath), server);
    assert.equal(recovered.statusOf("create-1"), "sent");
    const summary = await recovered.flush();

    assert.deepEqual(summary.failed, []);
    assert.deepEqual(summary.acked.sort(), ["create-1", "independent", "update-1"]);
    assert.equal(server.commitCount("create-1"), 1);
    assert.equal(server.received.filter((received) => received.id === "create-1").length, 2);
    assert.equal(recovered.resolvedId("temp:1"), "srv:1");

    const update = server.received.find((received) => received.id === "update-1");
    assert.deepEqual(update?.payload, { id: "srv:1", v: 2 });
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("reference to unknown temp id is never sent upstream", async () => {
  const server = new FakeServer();
  const engine = await ReplayEngine.open(new MemoryJournal(), server);

  await engine.enqueue(op("dangling", { payload: { id: "temp:missing" } }));
  await engine.enqueue(op("fine", { payload: { id: "srv:1" } }));

  const summary = await engine.flush();

  assert.deepEqual(summary.pending, ["dangling"]);
  assert.deepEqual(summary.acked, ["fine"]);
  assert.deepEqual(server.received.map((received) => received.id), ["fine"]);
});
