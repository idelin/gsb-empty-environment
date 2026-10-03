import { ENTRY_NOOP, type LogEntry } from '../codec/logEntry.ts';
import { LogStore, type LogStoreOptions } from '../storage/logStore.ts';
import { Acceptor } from './acceptor.ts';
import type { SimulatedNetwork } from './network.ts';
import { LogicalClock, Proposer, type Clock } from './proposer.ts';
import {
  MSG_ACCEPT,
  MSG_COMMIT,
  MSG_PREPARE,
  type AcceptReq,
  type CommitNotice,
  type MessageType,
  type PrepareReq,
} from './types.ts';

export interface NodeOptions extends LogStoreOptions {
  leaseMs?: number;
  clock?: Clock;
}

/**
 * A Paxos node = persistent store + acceptor + proposer, wired into the
 * simulated network. `commitIndex` is the follower's view of the leader's
 * commit point, capped at the highest locally contiguous slot.
 */
export class PaxosNode {
  readonly store: LogStore;
  readonly acceptor: Acceptor;
  readonly proposer: Proposer;
  #commitIndex: number;

  readonly id: number;
  readonly peers: number[];
  readonly network: SimulatedNetwork;

  private constructor(id: number, peers: number[], network: SimulatedNetwork, store: LogStore, clock: Clock, leaseMs: number) {
    this.id = id;
    this.peers = peers;
    this.network = network;
    this.store = store;
    this.acceptor = new Acceptor(id, store, clock, leaseMs);
    this.proposer = new Proposer(id, peers, network, store, { leaseMs, clock });
    this.#commitIndex = store.commitIndex;
    network.register(id, (type, payload, from) => this.#handle(type, payload, from));
  }

  static async open(
    id: number,
    peers: number[],
    dir: string,
    network: SimulatedNetwork,
    options: NodeOptions = {},
  ): Promise<PaxosNode> {
    const store = await LogStore.open(dir, options);
    return new PaxosNode(id, peers, network, store, options.clock ?? new LogicalClock(), options.leaseMs ?? 10_000);
  }

  async #handle(type: MessageType, payload: unknown, _from: number): Promise<unknown> {
    switch (type) {
      case MSG_PREPARE:
        return this.acceptor.onPrepare(payload as PrepareReq);
      case MSG_ACCEPT:
        return this.acceptor.onAccept(payload as AcceptReq);
      case MSG_COMMIT: {
        const notice = payload as CommitNotice;
        const target = Math.min(notice.commitIndex, this.store.contiguousIndex);
        if (target > this.#commitIndex) {
          this.#commitIndex = target;
          await this.store.setCommitIndex(target);
        }
        return { ok: true };
      }
    }
  }

  get commitIndex(): number {
    return this.#commitIndex;
  }

  /** Committed prefix of the log: slots 1..commitIndex, never with holes. */
  committedLog(): LogEntry[] {
    const out: LogEntry[] = [];
    for (let i = 1; i <= this.#commitIndex; i++) {
      const e = this.store.get(i);
      if (!e) throw new Error(`committed slot ${i} missing on node ${this.id}`);
      out.push(e);
    }
    return out;
  }

  /** Convenience for tests/demos: "i:type:value" per committed slot. */
  committedSummary(): string {
    return this.committedLog()
      .map((e) => `${e.index}:${e.type === ENTRY_NOOP ? 'noop' : e.value.toString('utf8')}`)
      .join(' ');
  }

  async close(): Promise<void> {
    this.network.unregister(this.id);
    await this.store.close();
  }
}
