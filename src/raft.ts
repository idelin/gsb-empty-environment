import type { Clock } from './clock.ts';
import type { Network } from './network.ts';

export type NodeId = string;
export type Role = 'follower' | 'candidate' | 'leader';

export interface LogEntry {
  term: number;
  command: string;
}

export type Message =
  | { type: 'PreVote'; term: number; from: NodeId; lastLogIndex: number; lastLogTerm: number }
  | { type: 'PreVoteReply'; term: number; voteTerm: number; from: NodeId; granted: boolean }
  | { type: 'RequestVote'; term: number; from: NodeId; lastLogIndex: number; lastLogTerm: number }
  | { type: 'RequestVoteReply'; term: number; from: NodeId; granted: boolean }
  | {
      type: 'AppendEntries';
      term: number;
      from: NodeId;
      prevLogIndex: number;
      prevLogTerm: number;
      entries: LogEntry[];
      leaderCommit: number;
    }
  | { type: 'AppendEntriesReply'; term: number; from: NodeId; success: boolean; matchIndex: number };

export interface NodeOptions {
  id: NodeId;
  members: NodeId[];
  electionTimeout: number;
  heartbeatInterval: number;
  clock: Clock;
  network: Network;
}

/**
 * A Raft-style replicated-log node. All progress happens inside `onTick`
 * (timers) and `receive` (messages), both driven by the discrete test clock.
 */
export class RaftNode {
  readonly id: NodeId;
  private members: NodeId[];
  private electionTimeout: number;
  private heartbeatInterval: number;
  private clock: Clock;
  private network: Network;

  role: Role = 'follower';
  currentTerm = 0;
  /** Every term this node has entered; must be non-decreasing. */
  termHistory: number[] = [0];
  votedFor: NodeId | null = null;
  leaderId: NodeId | null = null;
  log: LogEntry[] = [];
  commitIndex = 0;
  applied: string[] = [];

  preVoteAttempts = 0;
  private preVoteTerm = 0;
  private preVotesGranted = new Set<NodeId>();
  private votesGranted = new Set<NodeId>();

  private lastContact = 0;
  private lastQuorumContact = 0;
  private lastHeartbeat = 0;
  private recentReplies = new Map<NodeId, number>();
  private nextIndex = new Map<NodeId, number>();
  private matchIndex = new Map<NodeId, number>();

  constructor(opts: NodeOptions) {
    this.id = opts.id;
    this.members = opts.members;
    this.electionTimeout = opts.electionTimeout;
    this.heartbeatInterval = opts.heartbeatInterval;
    this.clock = opts.clock;
    this.network = opts.network;
    this.network.register(this.id, (msg) => this.receive(msg));
  }

  private majority(): number {
    return Math.floor(this.members.length / 2) + 1;
  }

  private lastLogIndex(): number {
    return this.log.length;
  }

  private lastLogTerm(): number {
    return this.log.length === 0 ? 0 : this.log[this.log.length - 1].term;
  }

  private termAt(index: number): number {
    return index === 0 ? 0 : this.log[index - 1].term;
  }

  private setTerm(term: number): void {
    if (term < this.currentTerm) {
      throw new Error(`term regression on ${this.id}: ${this.currentTerm} -> ${term}`);
    }
    if (term > this.currentTerm) {
      this.currentTerm = term;
      this.termHistory.push(term);
      this.votedFor = null;
    }
  }

  private becomeFollower(term: number): void {
    this.setTerm(term);
    this.role = 'follower';
    this.votesGranted.clear();
    this.preVotesGranted.clear();
  }

  private send(to: NodeId, msg: Message): void {
    this.network.send(this.id, to, msg);
  }

  private broadcast(msg: (peer: NodeId) => Message): void {
    for (const peer of this.members) {
      if (peer !== this.id) this.send(peer, msg(peer));
    }
  }

  // ---------------------------------------------------------------- timers

  onTick(): void {
    const now = this.clock.now;
    if (this.role === 'leader') {
      if (now - this.lastHeartbeat >= this.heartbeatInterval) {
        this.lastHeartbeat = now;
        this.broadcastAppend();
      }
      // Leader lease: step down when a majority has been unreachable
      // for a whole election timeout.
      if (now - this.lastQuorumContact >= this.electionTimeout) {
        this.role = 'follower';
        this.leaderId = null;
        this.lastContact = now;
      }
      return;
    }
    if (now - this.lastContact >= this.electionTimeout) {
      this.lastContact = now; // do not spam pre-votes every tick
      this.startPreVote();
    }
  }

  // -------------------------------------------------------------- election

  private startPreVote(): void {
    // Pre-vote does NOT increment currentTerm: an isolated node keeps
    // re-probing without disturbing the cluster's term numbering.
    this.preVoteAttempts += 1;
    this.preVoteTerm = this.currentTerm + 1;
    this.preVotesGranted = new Set([this.id]);
    this.broadcast(() => ({
      type: 'PreVote',
      term: this.preVoteTerm,
      from: this.id,
      lastLogIndex: this.lastLogIndex(),
      lastLogTerm: this.lastLogTerm(),
    }));
  }

  private startElection(): void {
    this.role = 'candidate';
    this.setTerm(this.currentTerm + 1);
    this.votedFor = this.id;
    this.votesGranted = new Set([this.id]);
    this.lastContact = this.clock.now;
    this.broadcast(() => ({
      type: 'RequestVote',
      term: this.currentTerm,
      from: this.id,
      lastLogIndex: this.lastLogIndex(),
      lastLogTerm: this.lastLogTerm(),
    }));
  }

  private becomeLeader(): void {
    this.role = 'leader';
    this.leaderId = this.id;
    this.lastQuorumContact = this.clock.now;
    const next = this.lastLogIndex() + 1;
    this.nextIndex.clear();
    this.matchIndex.clear();
    this.recentReplies.clear();
    for (const peer of this.members) {
      this.nextIndex.set(peer, next);
      this.matchIndex.set(peer, peer === this.id ? this.lastLogIndex() : 0);
    }
    this.lastHeartbeat = this.clock.now;
    this.broadcastAppend();
  }

  private isUpToDate(index: number, term: number): boolean {
    const myTerm = this.lastLogTerm();
    return term > myTerm || (term === myTerm && index >= this.lastLogIndex());
  }

  // ------------------------------------------------------------ replication

  /** Client write; only valid on the leader. Returns the new entry index. */
  append(command: string): number {
    if (this.role !== 'leader') {
      throw new Error(`${this.id} is not the leader (role=${this.role})`);
    }
    this.log.push({ term: this.currentTerm, command });
    this.matchIndex.set(this.id, this.lastLogIndex());
    this.broadcastAppend();
    return this.lastLogIndex();
  }

  private appendMessage(peer: NodeId): Message {
    const next = this.nextIndex.get(peer) ?? this.lastLogIndex() + 1;
    const prevLogIndex = next - 1;
    return {
      type: 'AppendEntries',
      term: this.currentTerm,
      from: this.id,
      prevLogIndex,
      prevLogTerm: this.termAt(prevLogIndex),
      entries: this.log.slice(prevLogIndex),
      leaderCommit: this.commitIndex,
    };
  }

  private broadcastAppend(): void {
    this.broadcast((peer) => this.appendMessage(peer));
  }

  /** Safe truncation: committed entries are never removed. */
  private truncate(fromIndex: number): void {
    if (fromIndex <= this.commitIndex) {
      throw new Error(
        `safety violation on ${this.id}: attempt to truncate committed entry at index ${fromIndex} (commitIndex=${this.commitIndex})`,
      );
    }
    this.log.length = fromIndex - 1;
  }

  private setCommitIndex(index: number): void {
    if (index <= this.commitIndex) return;
    this.commitIndex = Math.min(index, this.log.length);
    while (this.applied.length < this.commitIndex) {
      this.applied.push(this.log[this.applied.length].command);
    }
  }

  private advanceCommit(): void {
    for (let n = this.lastLogIndex(); n > this.commitIndex; n--) {
      if (this.log[n - 1].term !== this.currentTerm) continue;
      let count = 0;
      for (const peer of this.members) {
        if ((this.matchIndex.get(peer) ?? 0) >= n) count += 1;
      }
      if (count >= this.majority()) {
        this.setCommitIndex(n);
        break;
      }
    }
  }

  // --------------------------------------------------------------- messages

  receive(msg: Message): void {
    switch (msg.type) {
      case 'PreVote':
        this.onPreVote(msg);
        break;
      case 'PreVoteReply':
        if (msg.term > this.currentTerm) this.becomeFollower(msg.term);
        if (msg.granted && msg.voteTerm === this.preVoteTerm && this.role === 'follower') {
          this.preVotesGranted.add(msg.from);
          if (this.preVotesGranted.size >= this.majority()) {
            this.startElection();
          }
        }
        break;
      case 'RequestVote':
        this.onRequestVote(msg);
        break;
      case 'RequestVoteReply':
        if (msg.term > this.currentTerm) {
          this.becomeFollower(msg.term);
          return;
        }
        if (this.role === 'candidate' && msg.term === this.currentTerm && msg.granted) {
          this.votesGranted.add(msg.from);
          if (this.votesGranted.size >= this.majority()) this.becomeLeader();
        }
        break;
      case 'AppendEntries':
        this.onAppendEntries(msg);
        break;
      case 'AppendEntriesReply':
        this.onAppendEntriesReply(msg);
        break;
    }
  }

  private onPreVote(msg: Extract<Message, { type: 'PreVote' }>): void {
    // Grant a pre-vote when the requester could legally start an election
    // for a term at least as new as ours and its log is up-to-date.
    // Crucially a partitioned node's pre-vote never reaches a majority,
    // so it cannot inflate its own term.
    const granted =
      msg.term >= this.currentTerm && this.isUpToDate(msg.lastLogIndex, msg.lastLogTerm);
    this.send(msg.from, {
      type: 'PreVoteReply',
      term: this.currentTerm,
      voteTerm: msg.term,
      from: this.id,
      granted,
    });
  }

  private onRequestVote(msg: Extract<Message, { type: 'RequestVote' }>): void {
    let granted = false;
    if (msg.term >= this.currentTerm) {
      if (msg.term > this.currentTerm) this.becomeFollower(msg.term);
      const mayVote = this.votedFor === null || this.votedFor === msg.from;
      if (mayVote && this.isUpToDate(msg.lastLogIndex, msg.lastLogTerm)) {
        this.votedFor = msg.from;
        this.lastContact = this.clock.now;
        granted = true;
      }
    }
    this.send(msg.from, { type: 'RequestVoteReply', term: this.currentTerm, from: this.id, granted });
  }

  private onAppendEntries(msg: Extract<Message, { type: 'AppendEntries' }>): void {
    if (msg.term < this.currentTerm) {
      this.send(msg.from, {
        type: 'AppendEntriesReply',
        term: this.currentTerm,
        from: this.id,
        success: false,
        matchIndex: 0,
      });
      return;
    }
    // A valid leader at term >= ours: step down / refresh contact.
    this.becomeFollower(msg.term);
    this.leaderId = msg.from;
    this.lastContact = this.clock.now;

    // Log consistency check.
    const consistent =
      msg.prevLogIndex <= this.log.length && this.termAt(msg.prevLogIndex) === msg.prevLogTerm;
    if (!consistent) {
      this.send(msg.from, {
        type: 'AppendEntriesReply',
        term: this.currentTerm,
        from: this.id,
        success: false,
        matchIndex: this.log.length,
      });
      return;
    }

    // Safe truncation + append of new entries.
    let index = msg.prevLogIndex;
    for (const entry of msg.entries) {
      index += 1;
      if (index <= this.log.length) {
        if (this.log[index - 1].term !== entry.term) {
          this.truncate(index);
          this.log.push(entry);
        }
      } else {
        this.log.push(entry);
      }
    }
    if (msg.leaderCommit > this.commitIndex) {
      this.setCommitIndex(Math.min(msg.leaderCommit, this.log.length));
    }
    this.send(msg.from, {
      type: 'AppendEntriesReply',
      term: this.currentTerm,
      from: this.id,
      success: true,
      matchIndex: msg.prevLogIndex + msg.entries.length,
    });
  }

  private onAppendEntriesReply(msg: Extract<Message, { type: 'AppendEntriesReply' }>): void {
    if (msg.term > this.currentTerm) {
      this.becomeFollower(msg.term);
      return;
    }
    if (this.role !== 'leader' || msg.term !== this.currentTerm) return;
    this.recentReplies.set(msg.from, this.clock.now);
    let fresh = 1; // self
    for (const [peer, at] of this.recentReplies) {
      if (peer !== this.id && this.clock.now - at < this.electionTimeout) fresh += 1;
    }
    if (fresh >= this.majority()) this.lastQuorumContact = this.clock.now;
    if (msg.success) {
      const matched = Math.max(this.matchIndex.get(msg.from) ?? 0, msg.matchIndex);
      this.matchIndex.set(msg.from, matched);
      this.nextIndex.set(msg.from, matched + 1);
      this.advanceCommit();
    } else {
      const next = Math.max(1, (this.nextIndex.get(msg.from) ?? 1) - 1);
      this.nextIndex.set(msg.from, next);
      this.send(msg.from, this.appendMessage(msg.from));
    }
  }
}
