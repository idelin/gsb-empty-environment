import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ReteNetwork } from "../src/index.ts";

function buildNetwork(): ReteNetwork {
  const network = new ReteNetwork();
  network.addRule({
    name: "adult-with-big-order",
    conditions: [
      {
        type: "User",
        where: { age: { gte: 18 } },
        bind: { name: "?user" },
      },
      {
        type: "Order",
        where: { amount: { gt: 50 } },
        bind: { user: "?user", id: "?orderId" },
      },
    ],
  });
  return network;
}

describe("RETE acceptance boundary", () => {
  it("activates on User(age=25)+Order(amount=100), clears on Order retraction", () => {
    const network = buildNetwork();
    assert.equal(network.getConflictSet().length, 0);

    network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
    assert.equal(network.getConflictSet().length, 0, "partial match must not fire");

    const orderId = network.addFact({
      type: "Order",
      attrs: { id: "O-1", user: "alice", amount: 100 },
    });

    const conflictSet = network.getConflictSet();
    assert.equal(conflictSet.length, 1, "beta join must activate the rule");
    assert.equal(conflictSet[0].rule, "adult-with-big-order");
    assert.equal(conflictSet[0].bindings.get("?user"), "alice");
    assert.equal(conflictSet[0].bindings.get("?orderId"), "O-1");
    assert.deepEqual(
      conflictSet[0].wmes.map((wme) => wme.fact.type),
      ["User", "Order"],
    );

    network.retractFact(orderId);
    assert.equal(
      network.getConflictSet().length,
      0,
      "negative token must precisely clear the pending activation",
    );
    assert.equal(network.stats().agendaSize, 0);
  });
});

describe("alpha network: constant filtering", () => {
  it("rejects facts failing constant tests", () => {
    const network = buildNetwork();
    network.addFact({ type: "User", attrs: { name: "bob", age: 10 } });
    network.addFact({
      type: "Order",
      attrs: { id: "O-2", user: "bob", amount: 100 },
    });
    assert.equal(network.getConflictSet().length, 0, "minor must be filtered");

    network.addFact({ type: "User", attrs: { name: "carol", age: 30 } });
    network.addFact({
      type: "Order",
      attrs: { id: "O-3", user: "carol", amount: 10 },
    });
    assert.equal(network.getConflictSet().length, 0, "cheap order must be filtered");
  });
});

describe("beta network: cross-fact join", () => {
  it("only joins facts whose shared variable matches", () => {
    const network = buildNetwork();
    network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
    network.addFact({
      type: "Order",
      attrs: { id: "O-9", user: "mallory", amount: 100 },
    });
    assert.equal(network.getConflictSet().length, 0, "join key mismatch must not fire");

    network.addFact({
      type: "Order",
      attrs: { id: "O-10", user: "alice", amount: 100 },
    });
    assert.equal(network.getConflictSet().length, 1);
  });

  it("produces the cartesian product of matching facts", () => {
    const network = buildNetwork();
    network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
    network.addFact({ type: "User", attrs: { name: "alice", age: 40 } });
    network.addFact({
      type: "Order",
      attrs: { id: "O-1", user: "alice", amount: 100 },
    });
    network.addFact({
      type: "Order",
      attrs: { id: "O-2", user: "alice", amount: 200 },
    });
    assert.equal(network.getConflictSet().length, 4);
  });
});

describe("negative-token retraction", () => {
  it("removes only the affected activation, keeping siblings intact", () => {
    const network = buildNetwork();
    network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
    const first = network.addFact({
      type: "Order",
      attrs: { id: "O-1", user: "alice", amount: 100 },
    });
    network.addFact({
      type: "Order",
      attrs: { id: "O-2", user: "alice", amount: 300 },
    });
    assert.equal(network.getConflictSet().length, 2);

    network.retractFact(first);
    const remaining = network.getConflictSet();
    assert.equal(remaining.length, 1, "sibling activation must survive");
    assert.equal(remaining[0].bindings.get("?orderId"), "O-2");
  });

  it("cascades when an upstream (left-side) fact is retracted", () => {
    const network = buildNetwork();
    const userId = network.addFact({
      type: "User",
      attrs: { name: "alice", age: 25 },
    });
    network.addFact({
      type: "Order",
      attrs: { id: "O-1", user: "alice", amount: 100 },
    });
    assert.equal(network.getConflictSet().length, 1);

    network.retractFact(userId);
    assert.equal(network.getConflictSet().length, 0);
    assert.equal(network.stats().wmes, 1, "order fact itself stays asserted");
  });

  it("retracting an unknown id is a no-op", () => {
    const network = buildNetwork();
    assert.equal(network.retractFact(999), false);
  });
});

describe("incremental behaviour", () => {
  it("activates rules added after facts were asserted", () => {
    const network = new ReteNetwork();
    network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
    network.addFact({
      type: "Order",
      attrs: { id: "O-1", user: "alice", amount: 100 },
    });
    network.addRule({
      name: "late-rule",
      conditions: [
        { type: "User", where: { age: { gte: 18 } }, bind: { name: "?u" } },
        { type: "Order", where: { amount: { gt: 50 } }, bind: { user: "?u" } },
      ],
    });
    assert.equal(network.getConflictSet().length, 1);
    assert.equal(network.getConflictSet()[0].rule, "late-rule");
  });

  it("re-activates when a matching fact is asserted again after retraction", () => {
    const network = buildNetwork();
    network.addFact({ type: "User", attrs: { name: "alice", age: 25 } });
    const orderId = network.addFact({
      type: "Order",
      attrs: { id: "O-1", user: "alice", amount: 100 },
    });
    network.retractFact(orderId);
    assert.equal(network.getConflictSet().length, 0);

    network.addFact({
      type: "Order",
      attrs: { id: "O-2", user: "alice", amount: 80 },
    });
    assert.equal(network.getConflictSet().length, 1);
  });
});
