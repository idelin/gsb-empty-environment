import { test } from "node:test";
import assert from "node:assert/strict";

import { MerkleTree } from "../src/merkle.ts";
import { Simulation } from "../src/simulation.ts";
import type { StateRecord } from "../src/state.ts";

const DOWN_NODES = ["node-3", "node-5"];
const GOSSIP_ROUNDS = 12;
const MAX_ANTI_ENTROPY_ROUNDS = 10;

function record(nodeId: string, incarnation = 1): StateRecord {
  return { nodeId, status: "UP", incarnation, updatedAt: 0 };
}

function buildSimulation(): Simulation {
  const sim = new Simulation({ nodeCount: 6, dropRate: 0.9, seed: 20261003 });
  sim.bootstrapCluster();
  assert.ok(sim.isConverged(), "cluster starts fully converged");
  for (const nodeId of DOWN_NODES) sim.markDown(nodeId);
  return sim;
}

test("merkle tree: identical tables yield identical root hashes", () => {
  const records = ["a", "b", "c"].map((id) => record(id));
  const left = new MerkleTree(records);
  const right = new MerkleTree([...records].reverse());
  assert.equal(left.hashAt(""), right.hashAt(""));
});

test("merkle tree: a single diverging record changes the root and localizes to one branch per level", () => {
  const base = ["a", "b", "c", "d"].map((id) => record(id));
  const modified = base.map((r) => (r.nodeId === "c" ? { ...r, status: "DOWN" as const, incarnation: 2 } : r));
  const left = new MerkleTree(base);
  const right = new MerkleTree(modified);
  assert.notEqual(left.hashAt(""), right.hashAt(""));

  // At every depth, exactly one child subtree differs (the one holding "c").
  let prefix = "";
  for (let level = 0; level < 4; level++) {
    const differing: string[] = [];
    for (let i = 0; i < 16; i++) {
      const child = prefix + i.toString(16);
      if (left.hashAt(child) !== right.hashAt(child)) differing.push(child);
    }
    assert.equal(differing.length, 1, `level ${level} should have exactly one diverging child`);
    prefix = differing[0];
  }
  assert.equal(prefix, MerkleTree.prefixOf("c"));
});

test("gossip under 90% packet loss leaves long-tail gaps", () => {
  const sim = buildSimulation();
  for (let round = 0; round < GOSSIP_ROUNDS; round++) sim.gossipRound();

  const { sent, dropped } = sim.network.stats;
  assert.ok(sent > 0, "gossip packets were sent");
  const ratio = dropped / sent;
  assert.ok(ratio >= 0.75, `expected most packets dropped, got ${(ratio * 100).toFixed(1)}%`);

  // The 90% loss must leave at least one node missing exactly the 2 DOWN events.
  const victims = sim.nodes.filter(
    (node) => JSON.stringify(sim.missingKeys(node)) === JSON.stringify([...DOWN_NODES].sort()),
  );
  assert.ok(
    victims.length >= 1,
    "expected at least one node missing exactly the 2 DOWN events after lossy gossip",
  );
});

test("anti-entropy precisely locates and repairs the 2 missed DOWN events", () => {
  const sim = buildSimulation();
  for (let round = 0; round < GOSSIP_ROUNDS; round++) sim.gossipRound();
  assert.ok(!sim.isConverged(), "gossip alone did not converge the cluster");

  const victim = sim.nodes.find(
    (node) => JSON.stringify(sim.missingKeys(node)) === JSON.stringify([...DOWN_NODES].sort()),
  );
  assert.ok(victim, "found a node missing exactly the 2 DOWN events");

  // Start the background anti-entropy loop and track what the victim repairs.
  const repairedByVictim = new Set<string>();
  let rounds = 0;
  while (!sim.isConverged() && rounds < MAX_ANTI_ENTROPY_ROUNDS) {
    const stats = sim.antiEntropyRound();
    rounds++;
    for (let i = 0; i < sim.nodes.length; i++) {
      if (sim.nodes[i] === victim && stats[i]) {
        for (const key of stats[i]!.repairedKeys) repairedByVictim.add(key);
      }
    }
  }

  assert.ok(sim.isConverged(), `cluster converged after ${rounds} anti-entropy rounds`);
  assert.deepEqual(
    [...repairedByVictim].sort(),
    [...DOWN_NODES].sort(),
    "anti-entropy repaired exactly the 2 missed DOWN events on the victim",
  );
  for (const nodeId of DOWN_NODES) {
    assert.equal(victim.table.get(nodeId)?.status, "DOWN", `${nodeId} is DOWN on the victim`);
  }
});

test("anti-entropy uses tree pruning: converged peers cost 1 hash comparison and 0 records", () => {
  const sim = buildSimulation();
  for (let round = 0; round < GOSSIP_ROUNDS; round++) sim.gossipRound();
  let rounds = 0;
  while (!sim.isConverged() && rounds < MAX_ANTI_ENTROPY_ROUNDS) {
    sim.antiEntropyRound();
    rounds++;
  }
  assert.ok(sim.isConverged());

  const stats = sim.nodes[0].syncWith(sim.nodes[1]);
  assert.equal(stats.hashComparisons, 1, "identical roots prune the whole tree");
  assert.equal(stats.recordsPulled, 0);
  assert.equal(stats.recordsPushed, 0);
});

test("anti-entropy transfers records only for diverging leaf buckets", () => {
  const sim = buildSimulation();
  // No gossip at all: node-0 syncs directly with node-3 (which holds its own
  // DOWN event). Only the leaf bucket containing node-3 may transfer records.
  const target = sim.nodes[0];
  const source = sim.nodes[3];
  const stats = target.syncWith(source);
  assert.deepEqual(stats.repairedKeys, ["node-3"]);
  assert.equal(stats.recordsPulled, 1);
  // Tree walk must be far cheaper than scanning: 1 root + 16 + 16 + 16 + 16
  // along a single diverging path, not one comparison per stored record.
  assert.ok(
    stats.hashComparisons <= 1 + 16 * 4,
    `expected pruned tree walk, got ${stats.hashComparisons} comparisons`,
  );
});
