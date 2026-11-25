import test from "node:test";
import assert from "node:assert/strict";
import {
  enumerateAllSimplePaths,
  enumerateCompatibleEndorsementSets,
  enumerateSimplePaths,
  evaluateTrust,
  normalizeSnapshot,
  pathsConflict,
} from "../src/index.ts";
import { at, key, sig, snapshot, uid } from "./helpers.ts";

function fivePathSnapshot() {
  return snapshot({
    keys: ["R", "T", "X1", "X2", "A", "B", "C", "D"].map((item) => key(item)),
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [
      sig("p1a", "R", "X1", { depth: 2, weight: 10, adminDomain: "X" }),
      sig("p1b", "X1", "X2", { depth: 1, weight: 10, adminDomain: "Y" }),
      sig("p1c", "X2", "T", { depth: 0, weight: 10 }),
      sig("p2a", "R", "A", { depth: 1, weight: 9, adminDomain: "X" }),
      sig("p2b", "A", "T", { depth: 0, weight: 9 }),
      sig("p3a", "R", "B", { depth: 1, weight: 9, adminDomain: "Y" }),
      sig("p3b", "B", "T", { depth: 0, weight: 9 }),
      sig("p4a", "R", "C", { depth: 1, weight: 8, adminDomain: "W" }),
      sig("p4b", "C", "T", { depth: 0, weight: 8 }),
      sig("p5a", "R", "D", { depth: 1, weight: 8, adminDomain: "Z" }),
      sig("p5b", "D", "T", { depth: 0, weight: 8 }),
    ],
  });
}

test("maximum independent weight is exact, not strongest-path-then-prune", () => {
  const snap = fivePathSnapshot();
  const ns = normalizeSnapshot(snap);
  const paths = enumerateSimplePaths(ns, {
    targetKey: "T",
    at: at(1),
    targetEmails: ["a@example.com"],
  });
  assert.equal(paths.length, 5);
  assert.equal(paths.every((path) => path.valid), true);

  const evaluation = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(1),
    threshold: 34,
  });
  assert.equal(evaluation.status, "trusted");
  assert.equal(evaluation.maxWeight, 34);
  assert.deepEqual(
    evaluation.selected.map((path) => path.pathId),
    ["p2a>p2b", "p3a>p3b", "p4a>p4b", "p5a>p5b"],
  );

  const above = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(1),
    threshold: 34.5,
  });
  assert.equal(above.status, "untrusted");
  assert.equal(above.maxWeight, 34);
});

test("all maximal compatible sets are enumerated for the small graph", () => {
  const ns = normalizeSnapshot(fivePathSnapshot());
  const paths = enumerateSimplePaths(ns, {
    targetKey: "T",
    at: at(1),
    targetEmails: ["a@example.com"],
  });
  const enumeration = enumerateCompatibleEndorsementSets(paths, { maximalOnly: true });
  assert.equal(enumeration.truncated, false);
  const weights = enumeration.sets.map((set) => set.weight);
  assert.deepEqual(weights, [34, 26]);
  assert.deepEqual(
    enumeration.sets[0].pathIds,
    ["p2a>p2b", "p3a>p3b", "p4a>p4b", "p5a>p5b"],
  );
  assert.deepEqual(
    enumeration.sets[1].pathIds,
    ["p1a>p1b>p1c", "p4a>p4b", "p5a>p5b"],
  );
});

test("a common revocation predecessor blocks otherwise disjoint domains", () => {
  const snap = snapshot({
    keys: ["R", "A", "B", "C", "T"].map((item) => key(item)),
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [
      sig("a1", "R", "A", {
        depth: 1,
        weight: 5,
        adminDomain: "X",
        revocationPredecessors: ["REVOKER"],
      }),
      sig("a2", "A", "T", { depth: 0, weight: 5 }),
      sig("b1", "R", "B", {
        depth: 1,
        weight: 4,
        adminDomain: "Y",
        revocationPredecessors: ["REVOKER"],
      }),
      sig("b2", "B", "T", { depth: 0, weight: 4 }),
      sig("c1", "R", "C", {
        depth: 1,
        weight: 6,
        adminDomain: "Z",
        revocationPredecessors: ["OTHER"],
      }),
      sig("c2", "C", "T", { depth: 0, weight: 6 }),
    ],
  });
  const ns = normalizeSnapshot(snap);
  const paths = enumerateSimplePaths(ns, {
    targetKey: "T",
    at: at(1),
    targetEmails: ["a@example.com"],
  });
  const byId = new Map(paths.map((path) => [path.pathId, path]));
  assert.equal(pathsConflict(byId.get("a1>a2")!, byId.get("b1>b2")!), true);
  assert.equal(pathsConflict(byId.get("a1>a2")!, byId.get("c1>c2")!), false);

  const evaluation = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(1),
    threshold: 11,
  });
  assert.equal(evaluation.status, "trusted");
  assert.equal(evaluation.maxWeight, 11);
  assert.deepEqual(evaluation.selected.map((path) => path.pathId), ["a1>a2", "c1>c2"]);
});

test("cycles are not re-traversed and cannot inflate weight", () => {
  const snap = snapshot({
    keys: ["R", "A", "B", "T"].map((item) => key(item)),
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [
      sig("e1", "R", "A", { depth: 5, weight: 5, adminDomain: "X" }),
      sig("e2", "A", "B", { depth: 4, weight: 4, adminDomain: "Y" }),
      sig("e3", "B", "A", { depth: 3, weight: 3, adminDomain: "Z" }),
      sig("e4", "B", "T", { depth: 0, weight: 2 }),
      sig("e5", "A", "T", { depth: 0, weight: 1 }),
    ],
  });
  const ns = normalizeSnapshot(snap);
  const paths = enumerateSimplePaths(ns, {
    targetKey: "T",
    at: at(1),
    targetEmails: ["a@example.com"],
  });
  assert.deepEqual(
    paths.map((path) => path.pathId),
    ["e1>e2>e4", "e1>e5"],
  );
  const allPaths = enumerateAllSimplePaths(ns, { at: at(1) });
  assert.deepEqual(
    allPaths.map((path) => path.pathId),
    ["e1", "e1>e2", "e1>e2>e4", "e1>e5"],
  );
  for (const path of paths) {
    assert.equal(new Set(path.nodes).size, path.nodes.length);
  }
  const evaluation = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(1),
    threshold: 1,
  });
  assert.equal(evaluation.maxWeight, 2);
});
