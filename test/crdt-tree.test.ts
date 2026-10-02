import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { Replica, type BaseNode, type MoveOp } from "../src/index.ts";

const BASE: BaseNode[] = [
  { id: "root", name: "", parent: null },
  { id: "dirA", name: "dirA", parent: "root" },
  { id: "dirB", name: "dirB", parent: "root" },
  { id: "dirC", name: "dirC", parent: "root" },
  { id: "fileA1", name: "notes.txt", parent: "dirA" },
];

function makePair(): [Replica, Replica] {
  return [new Replica("node1", BASE), new Replica("node2", BASE)];
}

function assertConverged(a: Replica, b: Replica): void {
  assert.deepEqual(
    [...a.state.values()].map((n) => [n.id, n.name, n.parent]),
    [...b.state.values()].map((n) => [n.id, n.name, n.parent]),
  );
}

function assertNoOrphans(r: Replica): void {
  const reachable = r.tree.reachableFrom("root");
  for (const id of r.state.keys()) {
    assert.ok(reachable.has(id), `node ${id} is orphaned (data loss)`);
  }
}

describe("basic move and rename", () => {
  it("moves a directory under another directory", () => {
    const [n1] = makePair();
    n1.move("dirA", "dirB");
    assert.equal(n1.pathOf("dirA"), "/dirB/dirA");
    assert.equal(n1.pathOf("fileA1"), "/dirB/dirA/notes.txt");
  });

  it("renames a node in place", () => {
    const [n1] = makePair();
    n1.rename("dirA", "archive");
    assert.equal(n1.pathOf("dirA"), "/archive");
    assert.equal(n1.pathOf("fileA1"), "/archive/notes.txt");
  });

  it("rejects moving a node into itself", () => {
    const [n1] = makePair();
    n1.move("dirA", "dirA");
    assert.equal(n1.pathOf("dirA"), "/dirA");
    const verdict = n1.tree.verdicts.at(-1)!;
    assert.equal(verdict.applied, false);
  });
});

describe("acceptance: concurrent A->B and B->A cycle arbitration", () => {
  it("undoes the later op, converges, and loses no directory", () => {
    const [n1, n2] = makePair();

    // Concurrent, disconnected operations.
    const op1 = n1.move("dirA", "dirB"); // node1: /dirA -> /dirB/dirA
    const op2 = n2.move("dirB", "dirA"); // node2: /dirB -> /dirA/dirB
    assert.equal(op1.time, 1);
    assert.equal(op2.time, 1); // concurrent: same Lamport time

    // Before sync each replica sees only its own move.
    assert.equal(n1.pathOf("dirA"), "/dirB/dirA");
    assert.equal(n2.pathOf("dirB"), "/dirA/dirB");

    n1.syncWith(n2);

    // Deterministic total order: (time=1, "node1") < (time=1, "node2"),
    // so node2's later op is the one undone on BOTH replicas.
    assertConverged(n1, n2);
    assert.equal(n1.pathOf("dirA"), "/dirB/dirA");
    assert.equal(n1.pathOf("dirB"), "/dirB");
    assert.equal(n1.pathOf("fileA1"), "/dirB/dirA/notes.txt");
    assertNoOrphans(n1);
    assertNoOrphans(n2);

    const verdicts = n1.tree.verdicts;
    assert.equal(verdicts.length, 2);
    assert.equal(verdicts[0]!.applied, true); // node1's op wins
    assert.equal(verdicts[1]!.applied, false); // node2's op undone
    assert.match(verdicts[1]!.reason!, /cycle/);
  });

  it("undoes the causally later op when clocks differ", () => {
    const [n1, n2] = makePair();
    n2.move("dirC", "dirB"); // advance node2's clock to 1
    const op1 = n1.move("dirA", "dirB"); // time 1
    const op2 = n2.move("dirB", "dirA"); // time 2 — later in total order
    assert.ok(op2.time > op1.time);

    n1.syncWith(n2);

    assertConverged(n1, n2);
    // node2's move of dirB is later, so it is undone; dirA wins under dirB.
    assert.equal(n1.pathOf("dirA"), "/dirB/dirA");
    assert.equal(n1.pathOf("dirB"), "/dirB");
    assert.equal(n1.pathOf("dirC"), "/dirB/dirC");
    assertNoOrphans(n1);
  });

  it("converges regardless of operation delivery order", () => {
    const [n1, n2] = makePair();
    const op1 = n1.move("dirA", "dirB");
    const op2 = n2.move("dirB", "dirA");

    const [a, b] = makePair();
    a.receive(op1);
    a.receive(op2); // log order
    b.receive(op2);
    b.receive(op1); // reversed order

    assertConverged(a, b);
    assert.equal(a.pathOf("dirA"), "/dirB/dirA");
    assertNoOrphans(a);
    assertNoOrphans(b);
  });

  it("is idempotent under duplicate delivery", () => {
    const [n1, n2] = makePair();
    const op1 = n1.move("dirA", "dirB");
    const op2 = n2.move("dirB", "dirA");
    n1.receive(op2);
    n1.receive(op2); // duplicate
    n1.receive(op1); // own op echoed back
    assert.equal(n1.tree.log.length, 2);
    assert.equal(n1.pathOf("dirA"), "/dirB/dirA");
  });
});

describe("deterministic total order", () => {
  it("breaks same-timestamp ties by replica id", () => {
    // Swap which replica issues which move; the winner must flip
    // deterministically with the replica id ordering.
    const n1 = new Replica("node1", BASE);
    const n2 = new Replica("node2", BASE);
    n1.move("dirB", "dirA"); // node1 now moves B into A
    n2.move("dirA", "dirB"); // node2 moves A into B
    n1.syncWith(n2);

    // node1's op sorts first, so dirB ends up inside dirA this time.
    assert.equal(n1.pathOf("dirB"), "/dirA/dirB");
    assert.equal(n1.pathOf("dirA"), "/dirA");
    assert.equal(n2.pathOf("dirB"), "/dirA/dirB");
    assertNoOrphans(n1);
  });

  it("breaks transitive three-way cycles (A->B, B->C, C->A)", () => {
    const n1 = new Replica("node1", BASE);
    const n2 = new Replica("node2", BASE);
    const n3 = new Replica("node3", BASE);
    n1.move("dirA", "dirB"); // t=1
    n2.move("dirB", "dirC"); // t=1
    n3.move("dirC", "dirA"); // t=1 — would close the loop
    n1.syncWith(n2);
    n1.syncWith(n3);
    n2.syncWith(n3);

    assertConverged(n1, n2);
    assertConverged(n1, n3);
    // Total order: node1 < node2 < node3 at t=1, so node3's op is undone.
    // Final chain: root -> dirC -> dirB -> dirA.
    assert.equal(n1.pathOf("dirA"), "/dirC/dirB/dirA");
    assert.equal(n1.pathOf("dirB"), "/dirC/dirB");
    assert.equal(n1.pathOf("dirC"), "/dirC");
    assertNoOrphans(n1);
  });
});

describe("rename conflicts", () => {
  it("resolves sibling name clashes deterministically without data loss", () => {
    const [n1, n2] = makePair();
    n1.rename("dirA", "shared");
    n2.rename("dirB", "shared");
    n1.syncWith(n2);

    assertConverged(n1, n2);
    const names = [...n1.state.values()]
      .filter((n) => n.parent === "root")
      .map((n) => n.name)
      .sort();
    // One keeps "shared", the other gets a deterministic "#<id>" suffix.
    assert.deepEqual(names, ["dirC", "shared", "shared#dirB"]);
    assertNoOrphans(n1);
  });
});
