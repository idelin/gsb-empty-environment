import { join } from "node:path";
import { Center } from "./center.ts";
import { EdgeNode } from "./node.ts";
import { Bus } from "./bus.ts";
import { Ledger } from "./ledger.ts";
import { Rng } from "./prng.ts";
import type { Message, NodeId, Tick } from "./types.ts";

export interface SimConfig {
  seed: number;
  ticks: number;
  windowLength: number;
  windowQuota: number;
  nodeIds: NodeId[];
  lossRate: number;
  dupRate: number;
  partitions: Array<{ from: Tick; to: Tick }>;
  restartAt: Partial<Record<NodeId, Tick>>;
  consumeProbability: number;
  conflictProbability: number;
  outDir: string;
}

export const DEFAULT_SIM_CONFIG: SimConfig = {
  seed: 1,
  ticks: 400,
  windowLength: 50,
  windowQuota: 120,
  nodeIds: ["A", "B"],
  lossRate: 0.1,
  dupRate: 0.05,
  partitions: [{ from: 120, to: 139 }],
  restartAt: { A: 170 },
  consumeProbability: 0.6,
  conflictProbability: 0.02,
  outDir: "data/run",
};

export interface SimResult {
  dir: string;
  busStats: { dropped: number; duplicated: number; inFlight: number };
}

function routeTo(msg: Message, from: string): string {
  if (from === "center") {
    switch (msg.kind) {
      case "GrantMsg":
        return msg.grant.nodeId;
      case "BorrowReject":
      case "ReturnAck":
        return msg.nodeId;
      default:
        throw new Error(`unroutable center message ${msg.kind}`);
    }
  }
  return "center";
}

export function runSimulation(config: SimConfig): SimResult {
  const rng = new Rng(config.seed);
  const bus = new Bus(rng, {
    lossRate: config.lossRate,
    dupRate: config.dupRate,
    minDelay: 1,
    maxDelay: 3,
    partitions: config.partitions,
  });

  const centerPath = join(config.outDir, "center.ledger.jsonl");
  let center = new Center({
    windowQuota: config.windowQuota,
    windowLength: config.windowLength,
    ledger: new Ledger(centerPath),
  });

  const nodePaths = new Map<NodeId, string>();
  const nodes = new Map<NodeId, EdgeNode>();
  for (const nodeId of config.nodeIds) {
    const path = join(config.outDir, `node-${nodeId}.ledger.jsonl`);
    nodePaths.set(nodeId, path);
    nodes.set(
      nodeId,
      new EdgeNode({ nodeId, windowLength: config.windowLength, ledger: new Ledger(path) }),
    );
  }

  const consumeSeq = new Map<NodeId, number>();
  const borrowSeq = new Map<NodeId, number>();
  const lastAccepted = new Map<NodeId, string>();

  const emit = (from: string, outputs: Array<{ to: string; msg: Message }>, tick: Tick) => {
    for (const out of outputs) bus.send(out.msg, from, out.to, tick);
  };

  for (let tick = 0; tick < config.ticks; tick++) {
    // Scheduled crash-recovery: rebuild the node purely from its persisted ledger.
    for (const nodeId of config.nodeIds) {
      if (config.restartAt[nodeId] === tick) {
        nodes.set(
          nodeId,
          EdgeNode.restore(nodePaths.get(nodeId)!, {
            nodeId,
            windowLength: config.windowLength,
          }),
        );
      }
    }

    // 1. Deliver due messages.
    for (const env of bus.deliver(tick)) {
      if (env.to === "center") {
        const out = center.receive(env.msg, tick);
        for (const msg of out) bus.send(msg, "center", routeTo(msg, "center"), tick);
      } else {
        const node = nodes.get(env.to);
        if (!node) continue;
        const out = node.receive(env.msg, tick);
        emit(env.to, out.outbox, tick);
      }
    }

    // 2. Expiry sweep and retransmissions (local atomic decisions).
    for (const nodeId of config.nodeIds) {
      const node = nodes.get(nodeId)!;
      emit(nodeId, node.sweep(tick).outbox, tick);
      emit(nodeId, node.retransmit(tick).outbox, tick);
    }

    // 3. Workload: random consumes, borrow on shortage, occasional cost conflicts.
    for (const nodeId of config.nodeIds) {
      const node = nodes.get(nodeId)!;
      const posInWindow = tick % config.windowLength;
      // Proactive borrow at window start so quota is on hand before a partition.
      if (posInWindow === 0 && node.available(tick) < 15 && !node.hasPendingBorrow()) {
        const seq = borrowSeq.get(nodeId) ?? 0;
        borrowSeq.set(nodeId, seq + 1);
        emit(nodeId, node.borrow(`bor-${nodeId}-${seq}`, 30 + rng.int(30), tick).outbox, tick);
        continue;
      }
      // End-of-window borrow: the grant arrives after its expiry tick, which
      // deterministically exercises the late-grant return path.
      if (posInWindow === config.windowLength - 1 && !node.hasPendingBorrow()) {
        const seq = borrowSeq.get(nodeId) ?? 0;
        borrowSeq.set(nodeId, seq + 1);
        emit(nodeId, node.borrow(`bor-${nodeId}-${seq}`, 5 + rng.int(5), tick).outbox, tick);
        continue;
      }
      if (node.available(tick) === 0 && !node.hasPendingBorrow() && rng.chance(0.5)) {
        const seq = borrowSeq.get(nodeId) ?? 0;
        borrowSeq.set(nodeId, seq + 1);
        const amount = 20 + rng.int(40);
        emit(nodeId, node.borrow(`bor-${nodeId}-${seq}`, amount, tick).outbox, tick);
        continue;
      }
      if (rng.chance(config.consumeProbability)) {
        const seq = consumeSeq.get(nodeId) ?? 0;
        consumeSeq.set(nodeId, seq + 1);
        const requestId = `req-${nodeId}-${seq}`;
        const cost = 1 + rng.int(8);
        const { result, outbox } = node.consume(requestId, cost, tick);
        emit(nodeId, outbox, tick);
        if (result.status === "accepted") lastAccepted.set(nodeId, requestId);
        if (result.status === "rejected" && !node.hasPendingBorrow()) {
          const bseq = borrowSeq.get(nodeId) ?? 0;
          borrowSeq.set(nodeId, bseq + 1);
          const amount = 20 + rng.int(40);
          emit(nodeId, node.borrow(`bor-${nodeId}-${bseq}`, amount, tick).outbox, tick);
        }
        // Fault injection: reuse the last accepted request id with a different cost.
        const prior = lastAccepted.get(nodeId);
        if (prior && rng.chance(config.conflictProbability)) {
          node.consume(prior, cost + 1, tick);
        }
      }
    }
  }

  return {
    dir: config.outDir,
    busStats: { dropped: bus.dropped, duplicated: bus.duplicated, inFlight: bus.inFlight },
  };
}
