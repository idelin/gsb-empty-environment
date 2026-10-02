/**
 * View-change (Raft-style) log replicator — single process, discrete time.
 *
 * Safety properties implemented here:
 *  - currentTerm is monotonically non-decreasing per node (observeTerm).
 *  - Pre-vote phase: a server only increments its term when a majority
 *    indicates it would vote for it, so partitioned nodes cannot inflate
 *    their term and disrupt the cluster on rejoin.
 *  - Leader lease: a server that heard from a live leader within its
 *    election timeout refuses pre-votes, keeping a healthy leader stable.
 *  - Log consistency check on AppendEntries (prevLogIndex/prevLogTerm).
 *  - Safe truncation: conflicting *uncommitted* entries are overwritten;
 *    entries at or below commitIndex are never truncated (violation => reject).
 *  - Commit rule: a leader only advances commitIndex to entries of its own
 *    term replicated on a majority (older entries commit indirectly).
 *
 * There are no timers, sockets or randomness: the test clock drives
 * tick(), and the harness delivers messages.
 */

export type NodeId = string;

export interface LogEntry {
  term: number;
  command: string;
}

export type Role = "follower" | "candidate" | "leader";

export type Message =
  | { kind: "PreVoteRequest"; term: number; candidateId: NodeId; lastLogIndex: number; lastLogTerm: number }
  | { kind: "PreVoteResponse"; term: number; from: NodeId; granted: boolean }
  | { kind: "RequestVoteRequest"; term: number; candidateId: NodeId; lastLogIndex: number; lastLogTerm: number }
  | { kind: "RequestVoteResponse"; term: number; from: NodeId; granted: boolean }
  | {
      kind: "AppendEntriesRequest";
      term: number;
      leaderId: NodeId;
      prevLogIndex: number;
      prevLogTerm: number;
      entries: LogEntry[];
      leaderCommit: number;
    }
  | { kind: "AppendEntriesResponse"; term: number; from: NodeId; success: boolean; matchIndex: number };

export interface RaftOptions {
  id: NodeId;
  peers: NodeId[];
  electionTimeout: number;
  heartbeatInterval: number;
  send: (to: NodeId, msg: Message) => void;
}

export class RaftNode {
  readonly id: NodeId;
  readonly peers: NodeId[];

  // Persistent state (must survive restart).
  currentTerm = 0;
  votedFor: NodeId | null = null;
  readonly log: LogEntry[] = []; // log[i] is entry at index i+1

  // Volatile state.
  commitIndex = 0;
  role: Role = "follower";
  leaderId: NodeId | null = null;

  private readonly electionTimeout: number;
  private readonly heartbeatInterval: number;
  private readonly send: (to: NodeId, msg: Message) => void;

  private electionElapsed = 0;
  private heartbeatElapsed = 0;
  private ticksSinceLeaderContact = Number.POSITIVE_INFINITY;

  private preVoteTerm = 0;
  private preVotesGranted = new Set<NodeId>();
  private votesGranted = new Set<NodeId>();

  private nextIndex = new Map<NodeId, number>();
  private matchIndex = new Map<NodeId, number>();

  constructor(opts: RaftOptions) {
    this.id = opts.id;
    this.peers = opts.peers;
    this.electionTimeout = opts.electionTimeout;
    this.heartbeatInterval = opts.heartbeatInterval;
    this.send = opts.send;
  }

  get quorum(): number {
    return Math.floor((this.peers.length + 1) / 2) + 1;
  }

  get lastLogIndex(): number {
    return this.log.length;
  }

  get lastLogTerm(): number {
    return this.log.length === 0 ? 0 : this.log[this.log.length - 1].term;
  }

  /** One discrete time step. Driven by the test clock. */
  tick(): void {
    if (this.role === "leader") {
      this.heartbeatElapsed += 1;
      if (this.heartbeatElapsed >= this.heartbeatInterval) {
        this.heartbeatElapsed = 0;
        // A leader that is actively heartbeating counts as a live leader.
        this.ticksSinceLeaderContact = 0;
        for (const peer of this.peers) this.sendAppendEntries(peer);
      }
      return;
    }
    this.electionElapsed += 1;
    this.ticksSinceLeaderContact += 1;
    if (this.electionElapsed >= this.electionTimeout) {
      this.electionElapsed = 0;
      this.startPreVote();
    }
  }

  /** Client proposal. Returns the new log index, or -1 if not leader. */
  propose(command: string): number {
    if (this.role !== "leader") return -1;
    this.log.push({ term: this.currentTerm, command });
    this.matchIndex.set(this.id, this.log.length);
    for (const peer of this.peers) this.sendAppendEntries(peer);
    return this.log.length;
  }

  /** Voluntarily give up leadership (lease revoked / admin action). */
  stepDown(): void {
    this.role = "follower";
    this.leaderId = null;
    // Cooldown: do not immediately re-campaign.
    this.electionElapsed = -this.electionTimeout;
  }

  receive(msg: Message): void {
    switch (msg.kind) {
      case "PreVoteRequest":
        this.onPreVoteRequest(msg);
        break;
      case "PreVoteResponse":
        this.onPreVoteResponse(msg);
        break;
      case "RequestVoteRequest":
        this.onRequestVoteRequest(msg);
        break;
      case "RequestVoteResponse":
        this.onRequestVoteResponse(msg);
        break;
      case "AppendEntriesRequest":
        this.onAppendEntriesRequest(msg);
        break;
      case "AppendEntriesResponse":
        this.onAppendEntriesResponse(msg);
        break;
    }
  }

  // ---------------------------------------------------------------- pre-vote

  private startPreVote(): void {
    this.preVoteTerm = this.currentTerm + 1;
    this.preVotesGranted = new Set([this.id]);
    for (const peer of this.peers) {
      this.send(peer, {
        kind: "PreVoteRequest",
        term: this.preVoteTerm,
        candidateId: this.id,
        lastLogIndex: this.lastLogIndex,
        lastLogTerm: this.lastLogTerm,
      });
    }
  }

  private onPreVoteRequest(req: Extract<Message, { kind: "PreVoteRequest" }>): void {
    const granted =
      req.term >= this.currentTerm &&
      this.ticksSinceLeaderContact >= this.electionTimeout &&
      this.isUpToDate(req.lastLogIndex, req.lastLogTerm);
    this.send(req.candidateId, { kind: "PreVoteResponse", term: req.term, from: this.id, granted });
  }

  private onPreVoteResponse(resp: Extract<Message, { kind: "PreVoteResponse" }>): void {
    if (resp.term !== this.preVoteTerm || this.role === "leader") return;
    if (resp.granted) {
      this.preVotesGranted.add(resp.from);
      if (this.preVotesGranted.size >= this.quorum) this.startElection();
    }
  }

  // -------------------------------------------------------------- election

  private startElection(): void {
    if (this.preVoteTerm <= this.currentTerm) return; // stale pre-vote round
    this.currentTerm = this.preVoteTerm; // monotonic: preVoteTerm > currentTerm
    this.role = "candidate";
    this.votedFor = this.id;
    this.votesGranted = new Set([this.id]);
    this.electionElapsed = 0;
    for (const peer of this.peers) {
      this.send(peer, {
        kind: "RequestVoteRequest",
        term: this.currentTerm,
        candidateId: this.id,
        lastLogIndex: this.lastLogIndex,
        lastLogTerm: this.lastLogTerm,
      });
    }
  }

  private onRequestVoteRequest(req: Extract<Message, { kind: "RequestVoteRequest" }>): void {
    if (req.term < this.currentTerm) {
      this.send(req.candidateId, { kind: "RequestVoteResponse", term: this.currentTerm, from: this.id, granted: false });
      return;
    }
    if (req.term > this.currentTerm) this.observeTerm(req.term);
    const granted =
      (this.votedFor === null || this.votedFor === req.candidateId) &&
      this.isUpToDate(req.lastLogIndex, req.lastLogTerm);
    if (granted) {
      this.votedFor = req.candidateId;
      this.electionElapsed = 0;
    }
    this.send(req.candidateId, { kind: "RequestVoteResponse", term: this.currentTerm, from: this.id, granted });
  }

  private onRequestVoteResponse(resp: Extract<Message, { kind: "RequestVoteResponse" }>): void {
    if (resp.term > this.currentTerm) {
      this.observeTerm(resp.term);
      return;
    }
    if (this.role !== "candidate" || resp.term !== this.currentTerm || !resp.granted) return;
    this.votesGranted.add(resp.from);
    if (this.votesGranted.size >= this.quorum) this.becomeLeader();
  }

  private becomeLeader(): void {
    this.role = "leader";
    this.leaderId = this.id;
    this.heartbeatElapsed = 0;
    this.ticksSinceLeaderContact = 0;
    for (const peer of this.peers) {
      this.nextIndex.set(peer, this.log.length + 1);
      this.matchIndex.set(peer, 0);
    }
    this.matchIndex.set(this.id, this.log.length);
    for (const peer of this.peers) this.sendAppendEntries(peer);
  }

  // ----------------------------------------------------------- replication

  private sendAppendEntries(to: NodeId): void {
    const next = this.nextIndex.get(to) ?? this.log.length + 1;
    const prevLogIndex = next - 1;
    const prevLogTerm = prevLogIndex === 0 ? 0 : this.log[prevLogIndex - 1].term;
    this.send(to, {
      kind: "AppendEntriesRequest",
      term: this.currentTerm,
      leaderId: this.id,
      prevLogIndex,
      prevLogTerm,
      entries: this.log.slice(prevLogIndex),
      leaderCommit: this.commitIndex,
    });
  }

  private onAppendEntriesRequest(req: Extract<Message, { kind: "AppendEntriesRequest" }>): void {
    const fail = (matchIndex: number) =>
      this.send(req.leaderId, { kind: "AppendEntriesResponse", term: this.currentTerm, from: this.id, success: false, matchIndex });

    if (req.term < this.currentTerm) {
      fail(0);
      return;
    }
    if (req.term > this.currentTerm) this.observeTerm(req.term);
    this.role = "follower";
    this.leaderId = req.leaderId;
    this.ticksSinceLeaderContact = 0;
    this.electionElapsed = 0;
    this.preVoteTerm = 0; // abandon any in-flight pre-vote

    // Consistency check: the log must contain prevLogIndex with prevLogTerm.
    if (req.prevLogIndex > this.log.length) {
      fail(this.log.length);
      return;
    }
    if (req.prevLogIndex > 0 && this.log[req.prevLogIndex - 1].term !== req.prevLogTerm) {
      fail(req.prevLogIndex - 1);
      return;
    }

    // Merge entries, truncating conflicting *uncommitted* suffix.
    let index = req.prevLogIndex;
    for (const entry of req.entries) {
      index += 1;
      const existing = this.log[index - 1];
      if (existing === undefined) {
        this.log.push(entry);
      } else if (existing.term !== entry.term) {
        if (index <= this.commitIndex) {
          // Safety: never truncate committed entries.
          fail(this.commitIndex);
          return;
        }
        this.log.length = index - 1;
        this.log.push(entry);
      }
    }

    if (req.leaderCommit > this.commitIndex) {
      this.commitIndex = Math.min(req.leaderCommit, this.log.length);
    }
    this.send(req.leaderId, {
      kind: "AppendEntriesResponse",
      term: this.currentTerm,
      from: this.id,
      success: true,
      matchIndex: req.prevLogIndex + req.entries.length,
    });
  }

  private onAppendEntriesResponse(resp: Extract<Message, { kind: "AppendEntriesResponse" }>): void {
    if (resp.term > this.currentTerm) {
      this.observeTerm(resp.term);
      return;
    }
    if (this.role !== "leader" || resp.term !== this.currentTerm) return;
    if (resp.success) {
      this.matchIndex.set(resp.from, resp.matchIndex);
      this.nextIndex.set(resp.from, resp.matchIndex + 1);
      this.advanceCommit();
    } else {
      const next = Math.max(1, (this.nextIndex.get(resp.from) ?? 1) - 1);
      this.nextIndex.set(resp.from, next);
      this.sendAppendEntries(resp.from);
    }
  }

  private advanceCommit(): void {
    for (let n = this.log.length; n > this.commitIndex; n--) {
      if (this.log[n - 1].term !== this.currentTerm) continue; // only own-term entries
      let count = 1; // self
      for (const peer of this.peers) {
        if ((this.matchIndex.get(peer) ?? 0) >= n) count += 1;
      }
      if (count >= this.quorum) {
        this.commitIndex = n;
        for (const peer of this.peers) this.sendAppendEntries(peer); // propagate leaderCommit
        break;
      }
    }
  }

  // ---------------------------------------------------------------- helpers

  /** Monotonic term: only ever moves forward; resets vote on new term. */
  private observeTerm(term: number): void {
    if (term > this.currentTerm) {
      this.currentTerm = term;
      this.votedFor = null;
      this.role = "follower";
      this.leaderId = null;
    }
  }

  private isUpToDate(lastLogIndex: number, lastLogTerm: number): boolean {
    if (lastLogTerm !== this.lastLogTerm) return lastLogTerm > this.lastLogTerm;
    return lastLogIndex >= this.lastLogIndex;
  }
}
