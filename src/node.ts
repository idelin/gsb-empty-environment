import type { Clock } from "./clock.ts";
import type { Network } from "./network.ts";
import type {
  Lease,
  LeaseRequest,
  LeaseResponse,
  Message,
  NodeId,
  NodeState,
  PreVoteRequest,
  PreVoteResponse,
  WriteAck,
} from "./types.ts";

export interface NodeConfig {
  id: NodeId;
  clusterSize: number;
  /** How long a lease grant is valid, in cluster-clock ms. */
  leaseTtlMs: number;
  /** Follower starts an election after this long without leader contact. */
  electionTimeoutMs: number;
  /** Leader renews once the remaining lease drops to this many ms. */
  renewBeforeMs: number;
}

/**
 * A node in the lease-arbitrated cluster.
 *
 * Safety rule (split-brain prevention): a node grants its lease vote to at
 * most one holder at a time and never grants to a different holder while a
 * previous grant is still unexpired — even for a higher term. Combined with
 * the quorum requirement (floor(n/2)+1 grants), any two quorums intersect,
 * so two valid leaders can never coexist.
 */
export class Node {
  readonly id: NodeId;
  state: NodeState = "follower";
  term = 0;
  /** Simulated committed writes, in order. */
  readonly log: unknown[] = [];
  /** Timestamped event trace, handy for demos and debugging. */
  readonly events: string[] = [];

  private heldLease: Lease | null = null;
  private grantedLease: Lease | null = null;
  private votes = new Map<NodeId, number>();
  private preVoteTerm = 0;
  private preVoteCount = 0;
  private lastLeaderContact = 0;

  private readonly clock: Clock;
  private readonly network: Network;
  private readonly config: NodeConfig;
  private readonly quorum: number;

  constructor(clock: Clock, network: Network, config: NodeConfig) {
    this.clock = clock;
    this.network = network;
    this.config = config;
    this.id = config.id;
    this.quorum = Math.floor(config.clusterSize / 2) + 1;
  }

  private emit(message: string): void {
    this.events.push(`[t=${this.clock.now()}ms] ${this.id}: ${message}`);
  }

  /** Lease this node currently holds as leader, if any. */
  lease(): Lease | null {
    return this.heldLease ? { ...this.heldLease } : null;
  }

  /** Lease this node has granted as a voter, if any. */
  granted(): Lease | null {
    return this.grantedLease ? { ...this.grantedLease } : null;
  }

  /** True only while this node may serve writes. */
  isServingLeader(): boolean {
    return (
      this.state === "leader" &&
      this.heldLease !== null &&
      this.clock.now() < this.heldLease.expiresAt
    );
  }

  /**
   * Pre-vote phase: probe whether a quorum could grant a lease for the next
   * term, without incrementing our term or disturbing anyone. Only a
   * successful pre-vote leads to a real election. This keeps isolated or
   * asymmetrically partitioned nodes from disrupting the healthy majority.
   */
  startElection(): void {
    this.lastLeaderContact = this.clock.now();
    this.preVoteTerm = this.term + 1;
    this.preVoteCount = 1;
    this.emit(`pre-votes for term ${this.preVoteTerm}`);
    this.network.broadcast(
      { type: "PreVoteRequest", from: this.id, term: this.preVoteTerm },
      this.peerIds(),
    );
    if (this.preVoteCount >= this.quorum) this.beginElection();
  }

  private beginElection(): void {
    if (this.state === "leader") return;
    const now = this.clock.now();
    this.term = this.preVoteTerm;
    this.state = "candidate";
    this.heldLease = null;
    this.votes.clear();
    this.grantedLease = {
      holderId: this.id,
      term: this.term,
      expiresAt: now + this.config.leaseTtlMs,
    };
    this.votes.set(this.id, this.grantedLease.expiresAt);
    this.emit(`starts election for term ${this.term}`);
    this.network.broadcast(
      { type: "LeaseRequest", from: this.id, term: this.term },
      this.peerIds(),
    );
    this.maybeBecomeLeader();
  }

  /** Leader asks its quorum to extend the lease. */
  renewLease(): void {
    if (this.state !== "leader" || !this.heldLease) return;
    const now = this.clock.now();
    this.votes.clear();
    this.grantedLease = {
      holderId: this.id,
      term: this.term,
      expiresAt: now + this.config.leaseTtlMs,
    };
    this.votes.set(this.id, this.grantedLease.expiresAt);
    this.network.broadcast(
      { type: "LeaseRequest", from: this.id, term: this.term },
      this.peerIds(),
    );
    if (this.votes.size >= this.quorum) {
      this.heldLease.expiresAt = Math.min(...this.votes.values());
      this.emit(
        `renewed lease for term ${this.term} until ${this.heldLease.expiresAt}ms ` +
          `(${this.votes.size}/${this.config.clusterSize} grants)`,
      );
    } else {
      this.emit(
        `renewal FAILED: ${this.votes.size} grant(s), quorum is ${this.quorum}; ` +
          `lease still expires at ${this.heldLease.expiresAt}ms`,
      );
    }
  }

  /** Serve a write. Throws unless the lease is valid right now. */
  write(value: unknown): WriteAck {
    if (!this.isServingLeader()) {
      throw new Error(
        `${this.id}: write rejected (state=${this.state}, lease expired or none)`,
      );
    }
    const ack: WriteAck = { term: this.term, index: this.log.length, value };
    this.log.push(value);
    this.emit(`accepted write #${ack.index} (fencing term=${ack.term})`);
    return ack;
  }

  /** Called by the cluster harness as simulated time advances. */
  onTick(): void {
    const now = this.clock.now();
    if (this.state === "leader" && this.heldLease) {
      if (now >= this.heldLease.expiresAt) {
        this.stepDown("lease expired");
      } else if (this.heldLease.expiresAt - now <= this.config.renewBeforeMs) {
        this.renewLease();
      }
      return;
    }
    if (now - this.lastLeaderContact >= this.config.electionTimeoutMs) {
      this.startElection();
    }
  }

  receive(message: Message): void {
    if (message.type === "LeaseRequest") this.handleLeaseRequest(message);
    else if (message.type === "LeaseResponse") this.handleLeaseResponse(message);
    else if (message.type === "PreVoteRequest") this.handlePreVoteRequest(message);
    else this.handlePreVoteResponse(message);
  }

  private handlePreVoteRequest(request: PreVoteRequest): void {
    const now = this.clock.now();
    const active =
      this.grantedLease !== null && now < this.grantedLease.expiresAt;
    const blocked = active && this.grantedLease!.holderId !== request.from;
    const granted = request.term >= this.term && !blocked;
    this.network.send(
      {
        type: "PreVoteResponse",
        from: this.id,
        term: request.term,
        granted,
        reason: granted ? undefined : blocked ? "lease-held" : "stale-term",
      },
      request.from,
    );
  }

  private handlePreVoteResponse(response: PreVoteResponse): void {
    if (response.term !== this.preVoteTerm || !response.granted) return;
    this.preVoteCount += 1;
    if (this.preVoteCount >= this.quorum) this.beginElection();
  }

  private handleLeaseRequest(request: LeaseRequest): void {
    const now = this.clock.now();
    const reply = (granted: boolean, reason?: string, expiresAt?: number) =>
      this.network.send(
        {
          type: "LeaseResponse",
          from: this.id,
          term: request.term,
          granted,
          reason,
          expiresAt,
        },
        request.from,
      );

    if (request.term < this.term) {
      reply(false, "stale-term");
      return;
    }

    const active =
      this.grantedLease !== null && now < this.grantedLease.expiresAt;
    if (active && this.grantedLease!.holderId !== request.from) {
      // Safety-critical: never grant while another holder's lease is valid.
      reply(false, "lease-held");
      return;
    }

    if (this.state === "leader") {
      this.stepDown(`granted lease to ${request.from} for term ${request.term}`);
    }
    this.state = "follower";
    this.term = request.term;
    this.grantedLease = {
      holderId: request.from,
      term: request.term,
      expiresAt: now + this.config.leaseTtlMs,
    };
    this.lastLeaderContact = now;
    reply(true, undefined, this.grantedLease.expiresAt);
  }

  private handleLeaseResponse(response: LeaseResponse): void {
    if (response.term !== this.term) return;
    if (!response.granted) {
      if (response.reason === "lease-held" && this.state === "candidate") {
        // Someone else holds a valid lease: stand down and wait it out.
        this.state = "follower";
        this.lastLeaderContact = this.clock.now();
        this.emit(`stands down: a peer holds an unexpired lease`);
      }
      return;
    }
    if (this.state !== "candidate" && this.state !== "leader") return;
    this.votes.set(response.from, response.expiresAt!);
    this.maybeBecomeLeader();
  }

  private maybeBecomeLeader(): void {
    if (this.state !== "candidate" || this.votes.size < this.quorum) return;
    const expiresAt = Math.min(...this.votes.values());
    this.state = "leader";
    this.heldLease = { holderId: this.id, term: this.term, expiresAt };
    this.emit(
      `becomes LEADER for term ${this.term} ` +
        `(quorum of ${this.quorum} reached, lease until ${expiresAt}ms)`,
    );
  }

  private stepDown(reason: string): void {
    this.state = "follower";
    this.heldLease = null;
    this.lastLeaderContact = this.clock.now();
    this.emit(`steps down (${reason}), term ${this.term}`);
  }

  private peerIds(): NodeId[] {
    const ids: NodeId[] = [];
    for (let i = 0; i < this.config.clusterSize; i += 1) {
      const id = `node-${i}`;
      if (id !== this.id) ids.push(id);
    }
    return ids;
  }
}
