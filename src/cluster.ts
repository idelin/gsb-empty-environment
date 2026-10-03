import { Network } from './network.ts';
import { PaxosNode } from './node.ts';
import type { SubmitResult } from './node.ts';
import type { Command, LogEntry, Message } from './types.ts';

export interface ClusterOptions {
  seed?: number;
  leaseDuration?: number;
}

/**
 * Test/demo harness: wires N PaxosNodes to a deterministic Network and
 * continuously checks the core safety invariant — no two nodes ever commit
 * different values for the same slot.
 */
export class Cluster {
  readonly network: Network;
  readonly nodes: PaxosNode[] = [];
  /** Every message ever sent (for protocol-level assertions). */
  readonly sentLog: Message[] = [];
  /** Safety violations detected at commit time (must always stay empty). */
  readonly inconsistencies: string[] = [];
  private readonly committedValues = new Map<number, string>();

  constructor(size = 5, opts: ClusterOptions = {}) {
    this.network = new Network(opts.seed ?? 1);
    const ids = Array.from({ length: size }, (_, i) => i);
    for (const id of ids) {
      const node = new PaxosNode({
        id,
        peers: ids,
        leaseDuration: opts.leaseDuration ?? 50,
        now: () => this.network.now,
        send: (msg) => {
          this.sentLog.push(msg);
          this.network.send(msg);
        },
        onCommit: (nodeId, entry) => this.checkCommit(nodeId, entry),
      });
      this.nodes.push(node);
      this.network.register(id, (msg) => node.receive(msg));
    }
  }

  private checkCommit(nodeId: number, entry: LogEntry): void {
    const key = JSON.stringify(entry.command);
    const existing = this.committedValues.get(entry.slot);
    if (existing === undefined) {
      this.committedValues.set(entry.slot, key);
    } else if (existing !== key) {
      this.inconsistencies.push(
        `slot ${entry.slot}: node ${nodeId} committed ${key}, but ${existing} was already committed`,
      );
    }
  }

  elect(id: number): void {
    this.nodes[id].startElection();
    this.network.runUntilIdle();
  }

  submit(id: number, command: Command): SubmitResult {
    return this.nodes[id].submit(command);
  }

  stabilize(): void {
    this.network.runUntilIdle();
  }

  partition(groupA: number[], groupB: number[]): void {
    this.network.partition(groupA, groupB);
  }

  heal(): void {
    this.network.heal();
  }

  committedLog(id: number): LogEntry[] {
    return this.nodes[id].getCommittedLog();
  }

  countMessages(type: Message['type'], since = 0): number {
    let count = 0;
    for (let i = since; i < this.sentLog.length; i++) {
      if (this.sentLog[i].type === type) count++;
    }
    return count;
  }

  /**
   * Assert the committed-log invariants across the whole cluster:
   * 1. no divergent value was ever committed for the same slot;
   * 2. every committed prefix is hole-free and exactly commitIndex long;
   * 3. any two committed logs are prefix-compatible (linearizable).
   */
  assertConverged(): void {
    if (this.inconsistencies.length > 0) {
      throw new Error('committed-value divergence:\n' + this.inconsistencies.join('\n'));
    }
    const logs = this.nodes.map((node) => node.getCommittedLog());
    this.nodes.forEach((node, i) => {
      if (logs[i].length !== node.getCommitIndex()) {
        throw new Error(`node ${i}: hole in committed prefix (commitIndex=${node.getCommitIndex()})`);
      }
    });
    for (let i = 0; i < logs.length; i++) {
      for (let j = i + 1; j < logs.length; j++) {
        const shorter = logs[i].length <= logs[j].length ? logs[i] : logs[j];
        const longer = logs[i].length <= logs[j].length ? logs[j] : logs[i];
        for (let k = 0; k < shorter.length; k++) {
          if (JSON.stringify(shorter[k].command) !== JSON.stringify(longer[k].command)) {
            throw new Error(`node ${i} and node ${j} diverge at slot ${k + 1}`);
          }
        }
      }
    }
  }
}
