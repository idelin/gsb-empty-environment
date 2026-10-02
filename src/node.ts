import { StateTable } from "./state.ts";
import type { StateRecord, NodeStatus } from "./state.ts";
import { MerkleTree, TREE_DEPTH } from "./merkle.ts";
import type { LossyNetwork, GossipReceiver } from "./network.ts";

export interface SyncStats {
  peer: string;
  /** Number of tree-hash comparisons performed while walking the Merkle tree. */
  hashComparisons: number;
  /** Records pulled from the peer that were actually applied locally. */
  recordsPulled: number;
  /** Local records pushed to the peer that it applied. */
  recordsPushed: number;
  /** nodeIds whose state this node repaired during the sync. */
  repairedKeys: string[];
}

/**
 * A cluster node running:
 *  - a lightweight gossip protocol (piggybacking recent events to a random peer), and
 *  - a low-frequency background anti-entropy coroutine that repairs missed
 *    state by walking both replicas' Merkle trees depth-first.
 */
export class ClusterNode implements GossipReceiver {
  readonly nodeId: string;
  readonly table = new StateTable();
  lastSync: SyncStats | null = null;

  private network: LossyNetwork;
  private rng: () => number;
  private recentEvents: StateRecord[] = [];
  private eventBufferSize: number;
  private tree: MerkleTree;

  constructor(
    nodeId: string,
    network: LossyNetwork,
    rng: () => number,
    eventBufferSize: number = 64,
  ) {
    this.nodeId = nodeId;
    this.network = network;
    this.rng = rng;
    this.eventBufferSize = eventBufferSize;
    this.tree = new MerkleTree([]);
    network.register(this);
  }

  /** Install the initial (UP) membership record without gossiping it. */
  bootstrap(record: StateRecord): void {
    this.table.apply(record);
    this.rebuildTree();
  }

  /** Change this node's own status; the event enters the gossip buffer. */
  markSelf(status: NodeStatus, incarnation: number, updatedAt: number): void {
    const record: StateRecord = { nodeId: this.nodeId, status, incarnation, updatedAt };
    if (this.table.apply(record)) {
      this.pushEvent(record);
      this.rebuildTree();
    }
  }

  /** One gossip round: piggyback recent events to a random peer. */
  gossipTick(): boolean {
    const peers = this.network.peersOf(this.nodeId);
    if (peers.length === 0 || this.recentEvents.length === 0) return false;
    const target = peers[Math.floor(this.rng() * peers.length)];
    return this.network.sendGossip(this.nodeId, target, this.recentEvents);
  }

  receiveGossip(events: StateRecord[]): void {
    let changed = false;
    for (const event of events) {
      if (this.table.apply(event)) {
        this.pushEvent(event);
        changed = true;
      }
    }
    if (changed) this.rebuildTree();
  }

  /** One anti-entropy round: full Merkle-tree sync with a random peer. */
  antiEntropyTick(): SyncStats | null {
    const peers = this.network.peersOf(this.nodeId);
    if (peers.length === 0) return null;
    const target = peers[Math.floor(this.rng() * peers.length)];
    return this.syncWith(this.network.lookup<ClusterNode>(target));
  }

  /**
   * Depth-first Merkle-tree reconciliation against `peer`.
   * Subtrees with equal hashes are pruned with a single comparison; only
   * diverging branches are descended, and only diverging leaf buckets
   * exchange actual records.
   */
  syncWith(peer: ClusterNode): SyncStats {
    const stats: SyncStats = {
      peer: peer.nodeId,
      hashComparisons: 0,
      recordsPulled: 0,
      recordsPushed: 0,
      repairedKeys: [],
    };
    const walk = (prefix: string): void => {
      stats.hashComparisons++;
      if (this.tree.hashAt(prefix) === peer.treeHashAt(prefix)) return;
      if (prefix.length === TREE_DEPTH) {
        const pulled = this.receiveRecords(peer.recordsForPrefix(prefix));
        stats.recordsPulled += pulled.length;
        stats.repairedKeys.push(...pulled);
        stats.recordsPushed += peer.receiveRecords(this.recordsForPrefix(prefix)).length;
        return;
      }
      for (let i = 0; i < 16; i++) walk(prefix + i.toString(16));
    };
    walk("");
    this.lastSync = stats;
    return stats;
  }

  treeHashAt(prefix: string): string {
    return this.tree.hashAt(prefix);
  }

  recordsForPrefix(prefix: string): StateRecord[] {
    return this.table
      .all()
      .filter((record) => MerkleTree.keyOf(record.nodeId).startsWith(prefix));
  }

  receiveRecords(records: StateRecord[]): string[] {
    const applied: string[] = [];
    for (const record of records) {
      if (this.table.apply(record)) {
        this.pushEvent(record);
        applied.push(record.nodeId);
      }
    }
    if (applied.length > 0) this.rebuildTree();
    return applied;
  }

  rootHash(): string {
    return this.tree.hashAt("");
  }

  isConvergedWith(peer: ClusterNode): boolean {
    return this.rootHash() === peer.rootHash();
  }

  private pushEvent(record: StateRecord): void {
    this.recentEvents.push({ ...record });
    if (this.recentEvents.length > this.eventBufferSize) {
      this.recentEvents.splice(0, this.recentEvents.length - this.eventBufferSize);
    }
  }

  private rebuildTree(): void {
    this.tree = new MerkleTree(this.table.all());
  }
}
