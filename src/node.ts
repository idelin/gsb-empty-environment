import { diffByDepth, MerkleTree, type ChildInfo, type DiffResult, type TreeAccessor } from './merkle.ts';
import { mulberry32 } from './rng.ts';
import type { GossipMessage, NodeId, NodeState } from './types.ts';
import type { LossyTransport } from './transport.ts';

const MAX_RECENT_EVENTS = 64;
const PIGGYBACK_LIMIT = 8;

export interface AntiEntropyStats {
  rounds: number;
  divergentRounds: number;
  keysRepaired: number;
  nodesCompared: number;
  roundTrips: number;
}

export interface SyncResult extends DiffResult {
  peer: NodeId;
  /** Keys this node actually applied (was missing or stale on). */
  repaired: NodeId[];
}

export class ClusterNode {
  readonly states = new Map<NodeId, NodeState>();
  private recentEvents: NodeState[] = [];
  private peers: NodeId[] = [];
  private directory!: Map<NodeId, ClusterNode>;
  private aeTimer: ReturnType<typeof setInterval> | null = null;
  private rng: () => number;
  alive = true;
  readonly aeStats: AntiEntropyStats = {
    rounds: 0,
    divergentRounds: 0,
    keysRepaired: 0,
    nodesCompared: 0,
    roundTrips: 0,
  };

  readonly id: NodeId;
  private transport: LossyTransport;

  constructor(id: NodeId, transport: LossyTransport, seed: number, membership: NodeState[]) {
    this.id = id;
    this.transport = transport;
    this.rng = mulberry32(seed);
    for (const s of membership) this.states.set(s.nodeId, { ...s });
    transport.register(id, (msg) => this.onGossip(msg));
  }

  bind(directory: Map<NodeId, ClusterNode>, peers: NodeId[]): void {
    this.directory = directory;
    this.peers = peers.filter((p) => p !== this.id);
  }

  // ---------------- Gossip: probabilistic, lossy, best-effort ----------------

  gossipRound(): void {
    if (!this.alive || this.peers.length === 0 || this.recentEvents.length === 0) return;
    const target = this.peers[Math.floor(this.rng() * this.peers.length)];
    this.transport.send(this.id, target, {
      kind: 'gossip',
      from: this.id,
      events: this.recentEvents.slice(-PIGGYBACK_LIMIT),
    });
  }

  private onGossip(msg: GossipMessage): void {
    if (!this.alive) return;
    for (const event of msg.events) this.merge(event);
  }

  /** Last-writer-wins merge by version. Returns true when local state changed. */
  merge(state: NodeState): boolean {
    const current = this.states.get(state.nodeId);
    if (current && current.version >= state.version) return false;
    const copy = { ...state };
    this.states.set(state.nodeId, copy);
    this.recentEvents.push(copy);
    if (this.recentEvents.length > MAX_RECENT_EVENTS) this.recentEvents.shift();
    return true;
  }

  /** Detector-side declaration that a peer went offline. */
  declareDead(nodeId: NodeId): void {
    const current = this.states.get(nodeId);
    this.merge({ nodeId, status: 'dead', version: (current?.version ?? 0) + 1, updatedBy: this.id });
  }

  shutdown(): void {
    this.alive = false;
    this.stopAntiEntropy();
  }

  // ------------- Anti-entropy: reliable direct RPC, low frequency -------------

  merkle(): MerkleTree {
    return new MerkleTree(this.states.values());
  }

  rootHash(): string {
    return this.merkle().rootHash;
  }

  childrenOf(path: number[]): ChildInfo[] {
    return this.merkle().childrenOf(path);
  }

  statesFor(keys: NodeId[]): NodeState[] {
    const out: NodeState[] = [];
    for (const key of keys) {
      const s = this.states.get(key);
      if (s) out.push({ ...s });
    }
    return out;
  }

  /**
   * One anti-entropy exchange against a peer: traverse both Merkle trees level
   * by level, pull the exact divergent keys, and push back anything newer we
   * hold (bidirectional repair).
   */
  syncWith(peerId: NodeId): SyncResult | null {
    if (!this.alive) return null;
    const peer = this.directory.get(peerId);
    if (!peer || !peer.alive || peer.id === this.id) return null;
    this.aeStats.rounds++;

    const localTree = this.merkle();
    const localAccessor: TreeAccessor = {
      rootHash: () => localTree.rootHash,
      childrenOf: (path) => localTree.childrenOf(path),
    };
    const remoteAccessor: TreeAccessor = {
      rootHash: () => peer.rootHash(),
      childrenOf: (path) => {
        this.aeStats.roundTrips++;
        return peer.childrenOf(path);
      },
    };

    const diff = diffByDepth(localAccessor, remoteAccessor);
    this.aeStats.nodesCompared += diff.nodesCompared;
    if (diff.keys.length === 0) return { ...diff, peer: peerId, repaired: [] };
    this.aeStats.divergentRounds++;

    const repaired: NodeId[] = [];
    for (const s of peer.statesFor(diff.keys)) {
      if (this.merge(s)) {
        repaired.push(s.nodeId);
        this.aeStats.keysRepaired++;
      }
    }
    for (const s of this.statesFor(diff.keys)) peer.merge(s);
    return { ...diff, peer: peerId, repaired };
  }

  /** One background tick: reconcile with a random live peer. */
  antiEntropyTick(): void {
    if (this.peers.length === 0) return;
    const target = this.peers[Math.floor(this.rng() * this.peers.length)];
    this.syncWith(target);
  }

  /** Start the low-frequency background anti-entropy coroutine. */
  startAntiEntropy(intervalMs: number): void {
    this.stopAntiEntropy();
    this.aeTimer = setInterval(() => this.antiEntropyTick(), intervalMs);
    this.aeTimer.unref?.();
  }

  stopAntiEntropy(): void {
    if (this.aeTimer) clearInterval(this.aeTimer);
    this.aeTimer = null;
  }
}
