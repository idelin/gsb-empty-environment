// Test/demo harness: wires N PaxosNodes to one Transport with file-backed
// stores under a temp (or given) directory.

import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LogStore } from './logstore.ts';
import { PaxosNode, type PaxosOptions } from './paxos.ts';
import { Transport } from './transport.ts';

export class Cluster {
  readonly transport = new Transport();
  readonly nodes: PaxosNode[] = [];
  readonly dir: string;

  private constructor(dir: string) {
    this.dir = dir;
  }

  static create(count = 5, opts: PaxosOptions & { dir?: string; fsync?: boolean } = {}): Cluster {
    const dir = opts.dir ?? mkdtempSync(join(tmpdir(), 'paxos-cluster-'));
    const cluster = new Cluster(dir);
    const ids = Array.from({ length: count }, (_, i) => i + 1);
    for (const id of ids) {
      const store = LogStore.open(join(dir, `node-${id}.log`), { fsync: opts.fsync });
      cluster.nodes.push(new PaxosNode(id, store, cluster.transport, ids, opts));
    }
    return cluster;
  }

  node(id: number): PaxosNode {
    const n = this.nodes.find((x) => x.id === id);
    if (!n) throw new Error(`no node ${id}`);
    return n;
  }

  partition(ids: number[]): void {
    this.transport.partition(ids, this.nodes.map((n) => n.id));
  }

  heal(): void {
    this.transport.heal();
  }

  async settle(): Promise<void> {
    // Transport drains only the message queue; accept/commit handling may
    // schedule further microtask-chain work (and further messages). Flush
    // both until the system is fully quiet.
    for (let i = 0; i < 5; i++) {
      await this.transport.settle();
      await new Promise((r) => setImmediate(r));
    }
    await this.transport.settle();
  }

  async stop(): Promise<void> {
    for (const n of this.nodes) await n.stop();
  }
}
