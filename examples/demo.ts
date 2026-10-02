/**
 * Runnable demo of the acceptance scenario:
 *
 *   1. A 6-node cluster starts fully converged (all members UP).
 *   2. node-3 and node-5 go DOWN; the events ride on lightweight gossip.
 *   3. The lossy network drops 90% of gossip piggyback packets, so the
 *      DOWN events never reach every node (probabilistic long-tail loss).
 *   4. The low-frequency background anti-entropy coroutine starts; by
 *      walking Merkle trees depth-first it pinpoints exactly the 2 missed
 *      DOWN events and repairs them, converging the cluster.
 *
 * Run with: npm run demo   (requires Node >= 22.6, recommended >= 23.6)
 */
import { Simulation } from "../src/simulation.ts";

const DOWN_NODES = ["node-3", "node-5"];
const GOSSIP_ROUNDS = 12;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function banner(title: string): void {
  console.log("\n=== " + title + " ===");
}

async function main(): Promise<void> {
  const sim = new Simulation({ nodeCount: 6, dropRate: 0.9, seed: 20261003 });

  banner("1. bootstrap: 6 nodes, all UP, fully converged");
  sim.bootstrapCluster();
  console.log(`converged: ${sim.isConverged()}`);

  banner("2. failures: node-3 and node-5 go DOWN");
  for (const nodeId of DOWN_NODES) {
    sim.markDown(nodeId);
    console.log(`${nodeId} marked DOWN (incarnation bumped)`);
  }

  banner("3. gossip phase: 12 rounds, 90% of piggyback packets dropped");
  for (let round = 1; round <= GOSSIP_ROUNDS; round++) {
    sim.gossipRound();
    await sleep(30); // simulate the gossip interval
  }
  const { sent, dropped, delivered } = sim.network.stats;
  console.log(
    `gossip packets: sent=${sent} delivered=${delivered} dropped=${dropped} ` +
      `(${(sim.network.dropRatio() * 100).toFixed(1)}% loss)`,
  );
  for (const node of sim.nodes) {
    const missing = sim.missingKeys(node);
    if (missing.length > 0) {
      console.log(`  ${node.nodeId} still missing: [${missing.join(", ")}]`);
    }
  }
  console.log(`converged after gossip alone: ${sim.isConverged()}`);

  banner("4. anti-entropy phase: background Merkle-tree repair starts");
  let round = 0;
  while (!sim.isConverged() && round < 10) {
    round++;
    const stats = sim.antiEntropyRound();
    await sleep(60); // low-frequency background cadence
    for (let i = 0; i < sim.nodes.length; i++) {
      const sync = stats[i];
      if (sync && sync.repairedKeys.length > 0) {
        console.log(
          `  round ${round}: ${sim.nodes[i].nodeId} <- ${sync.peer} ` +
            `repaired [${sync.repairedKeys.join(", ")}] ` +
            `(hash comparisons: ${sync.hashComparisons}, records pulled: ${sync.recordsPulled})`,
        );
      }
    }
  }

  banner("5. result");
  console.log(`converged after ${round} anti-entropy round(s): ${sim.isConverged()}`);
  for (const nodeId of DOWN_NODES) {
    const states = sim.nodes.map((node) => node.table.get(nodeId)?.status);
    console.log(`  ${nodeId} status across cluster: [${states.join(", ")}]`);
  }
}

await main();
