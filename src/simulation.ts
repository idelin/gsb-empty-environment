/**
 * Partition-heal simulation.
 *
 * A 6-node cluster splits into two 3-node sub-clusters. While partitioned,
 * both sides evolve their membership views independently (new members join,
 * self-refutations bump incarnations, false suspicions are recorded). When
 * the partition heals, bridge links are restored and the nodes run the
 * probe/digest/reconcile protocol once per round (SWIM-style: one peer
 * probe per node per round) until every digest in the cluster matches.
 */

import { digestToHex } from "./crc32.ts";
import { ClusterNode, type ProbeOutcome } from "./node.ts";
import type { MemberRecord } from "./membership.ts";

export const MAX_ROUNDS = 5;

export interface SimulationStats {
  mismatchesDetected: number;
  digestBytesExchanged: number;
  recordBytesExchanged: number;
  recordsPulled: number;
  recordsPushed: number;
}

export interface SimulationResult {
  rounds: number;
  converged: boolean;
  nodes: ClusterNode[];
  stats: SimulationStats;
  log: string[];
}

const LEFT = ["L1", "L2", "L3"];
const RIGHT = ["R1", "R2", "R3"];
const BASELINE_MEMBERS = [...LEFT, ...RIGHT];

function fullMesh(nodes: ClusterNode[], ids: string[]): void {
  for (const a of nodes) {
    for (const b of nodes) {
      if (a.id !== b.id && ids.includes(a.id) && ids.includes(b.id)) {
        a.connect(b);
      }
    }
  }
}

export function runSimulation(): SimulationResult {
  const log: string[] = [];
  const stats: SimulationStats = {
    mismatchesDetected: 0,
    digestBytesExchanged: 0,
    recordBytesExchanged: 0,
    recordsPulled: 0,
    recordsPushed: 0,
  };

  const nodes = [...LEFT, ...RIGHT].map((id) => new ClusterNode(id));
  const byId = new Map(nodes.map((node) => [node.id, node]));

  // --- Baseline: one healthy 6-node cluster, identical views. ---
  for (const node of nodes) {
    for (const member of BASELINE_MEMBERS) {
      node.updateLocal({ id: member, status: "alive", incarnation: 1 });
    }
  }
  fullMesh(nodes, BASELINE_MEMBERS);
  const baseline = digestToHex(nodes[0].table.digest);
  log.push(`[baseline] 6-node cluster, uniform digest=${baseline}`);

  // --- Partition: cut every inter-group link. ---
  for (const leftId of LEFT) {
    for (const rightId of RIGHT) {
      byId.get(leftId)!.disconnect(byId.get(rightId)!);
    }
  }
  log.push("[partition] links cut: {L1,L2,L3} x {R1,R2,R3}");

  // Left side evolves: L4 joins; L1 refutes an old suspicion (inc 3);
  // L2 restarts with a fresh incarnation; R3 is falsely suspected
  // from across the partition.
  const leftUpdates: MemberRecord[] = [
    { id: "L4", status: "alive", incarnation: 1 },
    { id: "L1", status: "alive", incarnation: 3 },
    { id: "L2", status: "alive", incarnation: 2 },
    { id: "R3", status: "suspect", incarnation: 2 },
  ];
  // Right side evolves: R4 joins; R3 refutes (inc 3); L1 is suspected.
  const rightUpdates: MemberRecord[] = [
    { id: "R4", status: "alive", incarnation: 1 },
    { id: "R3", status: "alive", incarnation: 3 },
    { id: "L1", status: "suspect", incarnation: 2 },
  ];
  for (const id of LEFT) {
    for (const record of leftUpdates) byId.get(id)!.updateLocal(record);
  }
  for (const id of RIGHT) {
    for (const record of rightUpdates) byId.get(id)!.updateLocal(record);
  }
  log.push(
    `[partition] left digest=${digestToHex(byId.get("L1")!.table.digest)} ` +
      `right digest=${digestToHex(byId.get("R1")!.table.digest)} (diverged)`,
  );

  // --- Heal: restore two bridge links between the sub-clusters. ---
  byId.get("L1")!.connect(byId.get("R1")!);
  byId.get("L3")!.connect(byId.get("R2")!);
  log.push("[heal] bridge links restored: L1<->R1, L3<->R2");

  const allEqual = (): boolean =>
    nodes.every((node) => node.table.digest === nodes[0].table.digest);

  // --- Probe rounds: each node probes one peer per round (round-robin). ---
  let rounds = 0;
  while (!allEqual() && rounds < MAX_ROUNDS) {
    rounds += 1;
    const events: string[] = [];
    for (const node of nodes) {
      const peerIds = node.peerIds();
      if (peerIds.length === 0) continue;
      const peerId = peerIds[(rounds - 1) % peerIds.length];
      const outcome: ProbeOutcome = node.probe(byId.get(peerId)!);
      stats.digestBytesExchanged += outcome.digestBytesExchanged;
      stats.recordBytesExchanged += outcome.recordBytesExchanged;
      stats.recordsPulled += outcome.recordsPulled;
      stats.recordsPushed += outcome.recordsPushed;
      if (!outcome.inSync) {
        stats.mismatchesDetected += 1;
        events.push(
          `${node.id}->${peerId} MISMATCH buckets=[${outcome.differingBuckets}] ` +
            `pull=${outcome.recordsPulled} push=${outcome.recordsPushed}`,
        );
      }
    }
    const digests = nodes
      .map((node) => `${node.id}:${digestToHex(node.table.digest)}`)
      .join(" ");
    log.push(
      `[round ${rounds}] ${events.length > 0 ? events.join("; ") : "no divergence detected"}`,
    );
    log.push(`[round ${rounds}] digests ${digests}`);
  }

  const converged = allEqual();
  log.push(
    converged
      ? `[converged] all ${nodes.length} nodes aligned after ${rounds} round(s), ` +
          `digest=${digestToHex(nodes[0].table.digest)}`
      : `[failed] not converged within ${MAX_ROUNDS} rounds`,
  );
  return { rounds, converged, nodes, stats, log };
}

export function formatMembership(node: ClusterNode): string[] {
  return node.table
    .snapshot()
    .map(
      (record) =>
        `  ${record.id.padEnd(4)} ${record.status.padEnd(8)} inc=${record.incarnation}`,
    );
}

const isMain = process.argv[1]?.endsWith("simulation.ts");
if (isMain) {
  const result = runSimulation();
  console.log("=== partition-heal divergence detection simulation ===");
  for (const line of result.log) console.log(line);
  console.log("");
  console.log(`rounds used        : ${result.rounds} (limit ${MAX_ROUNDS})`);
  console.log(`mismatches detected: ${result.stats.mismatchesDetected}`);
  console.log(`digest bytes moved : ${result.stats.digestBytesExchanged}`);
  console.log(`record bytes moved : ${result.stats.recordBytesExchanged}`);
  console.log(`records pulled     : ${result.stats.recordsPulled}`);
  console.log(`records pushed     : ${result.stats.recordsPushed}`);
  console.log("");
  console.log(`final membership view on every node (digest=${digestToHex(result.nodes[0].table.digest)}):`);
  for (const line of formatMembership(result.nodes[0])) console.log(line);
  if (!result.converged) process.exitCode = 1;
}
