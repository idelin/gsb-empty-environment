import assert from "node:assert/strict";
import { test } from "node:test";
import { Cluster, mulberry32 } from "../src/cluster.ts";
import { Node } from "../src/node.ts";

const CLUSTER_SIZE = 100;
const LOG2_PERIODS = Math.ceil(Math.log2(CLUSTER_SIZE));

function nodeIds(prefix: string, count: number): string[] {
  return Array.from(
    { length: count },
    (_, i) => `${prefix}${String(i + 1).padStart(3, "0")}`,
  );
}

function seedSteadyState(cluster: Cluster, ids: string[]): void {
  for (const id of ids) {
    const node = cluster.nodes.get(id)!;
    for (const other of ids) {
      node.members.apply({ type: "join", nodeId: other, incarnation: 0 });
    }
  }
}

test("acceptance: a new node's join is piggybacked and known by all 100 nodes within log2(N) periods", () => {
  const cluster = new Cluster(mulberry32(20261002));
  const existing = nodeIds("n", CLUSTER_SIZE - 1);
  for (const id of existing) cluster.createNode(id);
  seedSteadyState(cluster, existing);

  const newcomer = cluster.createNode("n100");
  cluster.join("n100", "n001");
  assert.equal(cluster.informedCount("n100"), 2);

  const firstPing = newcomer.makePing("n001");
  assert.ok(
    firstPing.piggyback.some(
      (e) => e.type === "join" && e.nodeId === "n100",
    ),
    "join event must be piggybacked on subsequent ping packets",
  );

  let convergedAt = -1;
  for (let p = 1; p <= LOG2_PERIODS + 2; p += 1) {
    cluster.runPeriod();
    if (cluster.informedCount("n100") === CLUSTER_SIZE) {
      convergedAt = p;
      break;
    }
  }

  assert.equal(cluster.informedCount("n100"), CLUSTER_SIZE);
  assert.ok(
    convergedAt > 0 && convergedAt <= LOG2_PERIODS + 2,
    `expected convergence within ~log2(N) periods, got ${convergedAt}`,
  );
  for (const id of [...existing, "n100"]) {
    assert.equal(cluster.nodes.get(id)!.members.statusOf("n100"), "alive");
  }

  const drainPeriods = newcomer.gossip.roundsLimit + 2;
  for (let p = 0; p < drainPeriods; p += 1) cluster.runPeriod();
  assert.equal(
    cluster.pendingGossipTotal(),
    0,
    "gossip queues must drain once events hit the max-rounds limit",
  );
});

test("suspect gossip propagates and the suspect refutes it with a higher incarnation", () => {
  const cluster = new Cluster(mulberry32(7));
  const ids = nodeIds("n", 10);
  for (const id of ids) cluster.createNode(id);
  seedSteadyState(cluster, ids);

  cluster.nodes.get("n001")!.suspectPeer("n005");
  for (let p = 0; p < 12; p += 1) cluster.runPeriod();

  for (const id of ids) {
    const record = cluster.nodes.get(id)!.members.get("n005")!;
    assert.equal(record.status, "alive");
    assert.equal(record.incarnation, 1);
  }
});

test("leave gossip propagates to the whole cluster", () => {
  const cluster = new Cluster(mulberry32(99));
  const ids = nodeIds("n", 20);
  for (const id of ids) cluster.createNode(id);
  seedSteadyState(cluster, ids);

  cluster.leave("n020");
  for (let p = 0; p < 15; p += 1) cluster.runPeriod();

  for (const id of ids.slice(0, -1)) {
    assert.equal(cluster.nodes.get(id)!.members.statusOf("n020"), "left");
  }
});

test("a node receiving a suspect about itself bumps its incarnation", () => {
  const node = new Node("me");
  node.handlePacket({
    kind: "ack",
    from: "peer",
    to: "me",
    seq: 1,
    piggyback: [{ type: "suspect", nodeId: "me", incarnation: 0 }],
  });
  assert.equal(node.members.get("me")!.incarnation, 1);
  assert.equal(node.members.statusOf("me"), "alive");
  const ping = node.makePing("peer");
  assert.ok(
    ping.piggyback.some(
      (e) => e.type === "alive" && e.nodeId === "me" && e.incarnation === 1,
    ),
  );
});
