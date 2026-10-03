import { Network } from './network';
import { digestOf, digestOfSnapshot, mergeMembers } from './util';
import { MemberInfo, MemberStatus, Message, NodeId, ViewSnapshot } from './types';

export type MergeState = 'IDLE' | 'PROPOSING' | 'RESPONDING';

export interface NodeConfig {
  probeInterval: number;
  pingTimeout: number;
  suspectTimeout: number;
  deadTimeout: number;
  rejoinInterval: number;
  mergeRetryInterval: number;
  mergeMaxRetries: number;
}

export const defaultConfig: NodeConfig = {
  probeInterval: 10,
  pingTimeout: 5,
  suspectTimeout: 40,
  deadTimeout: 80,
  rejoinInterval: 40,
  mergeRetryInterval: 15,
  mergeMaxRetries: 6,
};

interface PendingPing {
  sentAt: number;
  reqSentAt: number | null;
}

export class Node {
  readonly id: NodeId;
  clock = 0;
  epoch = 0;
  state: MergeState = 'IDLE';
  members = new Map<NodeId, MemberInfo>();
  readonly stats = { proposesSent: 0, acceptsSent: 0, commitsBroadcast: 0, commitsApplied: 0 };

  private readonly bootPeers: NodeId[];
  private pendingPings = new Map<NodeId, PendingPing>();
  private pendingReqs = new Map<NodeId, number>();
  private mergePeer: NodeId | null = null;
  private mergeRetries = 0;
  private lastProposeAt = 0;
  private respondPeer: NodeId | null = null;
  private respondSince = 0;
  private lastProbeAt = 0;
  private lastRejoinAt: number;
  private lastRebroadcastEpoch = -1;

  constructor(
    id: NodeId,
    bootPeers: NodeId[],
    private readonly network: Network,
    private readonly rng: () => number,
    private readonly config: NodeConfig,
    private readonly log?: (nodeId: NodeId, text: string) => void,
  ) {
    this.id = id;
    this.bootPeers = bootPeers;
    this.lastRejoinAt = -Math.floor(rng() * config.rejoinInterval);
    for (const peer of bootPeers) {
      this.members.set(peer, { id: peer, incarnation: 1, status: 'alive', statusChangedAt: 0 });
    }
  }

  get digest(): string {
    return digestOf(this.epoch, this.members);
  }

  memberIds(): string {
    return [...this.members.keys()].sort((a, b) => a - b).join(',');
  }

  aliveCount(): number {
    return [...this.members.values()].filter((m) => m.status === 'alive').length;
  }

  snapshot(): ViewSnapshot {
    return { epoch: this.epoch, members: [...this.members.values()].map((m) => ({ ...m })) };
  }

  // ---------------------------------------------------------------- tick ---

  tick(now: number): void {
    if (now - this.lastProbeAt >= this.config.probeInterval) {
      this.lastProbeAt = now;
      const target = this.pickAlivePeer([this.id]);
      if (target !== null) {
        this.pendingPings.set(target, { sentAt: now, reqSentAt: null });
        this.send({ type: 'ping', from: this.id, to: target, clock: this.bumpClock(), epoch: this.epoch, digest: this.digest, replyTo: this.id, origin: this.id });
      }
    }

    for (const [target, pending] of [...this.pendingPings]) {
      if (pending.reqSentAt === null && now - pending.sentAt >= this.config.pingTimeout) {
        const via = this.pickAlivePeer([this.id, target]);
        if (via !== null) {
          pending.reqSentAt = now;
          this.send({ type: 'ping-req', from: this.id, to: via, clock: this.bumpClock(), target });
        }
      } else if (pending.reqSentAt !== null && now - pending.reqSentAt >= this.config.pingTimeout) {
        this.pendingPings.delete(target);
        this.markSuspect(target, now);
      }
    }

    for (const m of [...this.members.values()]) {
      if (m.id === this.id) continue;
      if (m.status === 'suspect' && now - m.statusChangedAt >= this.config.suspectTimeout) {
        this.setStatus(m, 'dead', now);
      } else if (m.status === 'dead' && now - m.statusChangedAt >= this.config.deadTimeout) {
        this.members.delete(m.id);
        this.emit(`removed dead member ${m.id}; view=[${this.memberIds()}]`);
      }
    }

    for (const [target, sentAt] of [...this.pendingReqs]) {
      if (now - sentAt >= this.config.pingTimeout * 2) this.pendingReqs.delete(target);
    }

    if (now - this.lastRejoinAt >= this.config.rejoinInterval) {
      this.lastRejoinAt = now;
      const candidate = this.pickRejoinTarget();
      if (candidate !== null) {
        const via = this.pickAlivePeer([this.id, candidate]);
        if (via !== null) {
          this.pendingReqs.set(candidate, now);
          this.send({ type: 'ping-req', from: this.id, to: via, clock: this.bumpClock(), target: candidate });
        }
      }
    }

    if (this.state === 'PROPOSING' && this.mergePeer !== null) {
      if (now - this.lastProposeAt >= this.config.mergeRetryInterval) {
        if (this.mergeRetries >= this.config.mergeMaxRetries) {
          this.emit(`merge with ${this.mergePeer} timed out; backing off to IDLE`);
          this.state = 'IDLE';
          this.mergePeer = null;
          this.mergeRetries = 0;
        } else {
          this.mergeRetries++;
          this.sendPropose(this.mergePeer, now);
        }
      }
    } else if (this.state === 'RESPONDING') {
      if (now - this.respondSince >= this.config.mergeRetryInterval * this.config.mergeMaxRetries) {
        this.state = 'IDLE';
        this.respondPeer = null;
      }
    }
  }

  // ------------------------------------------------------------- receive ---

  receive(msg: Message, now: number): void {
    this.clock = Math.max(this.clock, msg.clock) + 1;
    switch (msg.type) {
      case 'ping':
        this.send({ type: 'ack', from: this.id, to: msg.replyTo, clock: this.bumpClock(), epoch: this.epoch, digest: this.digest, forOrigin: msg.origin });
        break;

      case 'ack':
        if (msg.forOrigin !== this.id) {
          this.send({ type: 'indirect-ack', from: this.id, to: msg.forOrigin, clock: this.bumpClock(), epoch: msg.epoch, digest: msg.digest, target: msg.from });
        } else {
          this.pendingPings.delete(msg.from);
          this.onPeerDigest(msg.from, msg.epoch, msg.digest, now);
        }
        break;

      case 'ping-req':
        this.send({ type: 'ping', from: this.id, to: msg.target, clock: this.bumpClock(), epoch: this.epoch, digest: this.digest, replyTo: this.id, origin: msg.from });
        break;

      case 'indirect-ack':
        this.pendingReqs.delete(msg.target);
        this.pendingPings.delete(msg.target);
        this.onPeerDigest(msg.target, msg.epoch, msg.digest, now);
        break;

      case 'merge-propose':
        this.onMergePropose(msg, now);
        break;

      case 'merge-accept':
        if (this.state !== 'PROPOSING' || this.mergePeer !== msg.from) break;
        this.members = mergeMembers(this.members, msg.view.members);
        this.epoch = Math.max(this.epoch, msg.epoch) + 1;
        this.refuteSelf();
        this.state = 'IDLE';
        this.mergePeer = null;
        this.stats.commitsBroadcast++;
        this.broadcastCommit();
        this.emit(`merged with ${msg.from}; epoch=${this.epoch} members=[${this.memberIds()}]`);
        break;

      case 'merge-commit':
        if (msg.epoch < this.epoch) break;
        if (msg.epoch === this.epoch && digestOfSnapshot(msg.view) === this.digest) break;
        this.applySnapshot(msg.view, msg.epoch);
        this.state = 'IDLE';
        this.mergePeer = null;
        this.respondPeer = null;
        this.stats.commitsApplied++;
        if (msg.epoch > this.lastRebroadcastEpoch) {
          this.lastRebroadcastEpoch = msg.epoch;
          this.stats.commitsBroadcast++;
          this.broadcastCommit();
        }
        break;

      case 'view-request':
        this.send({ type: 'view-sync', from: this.id, to: msg.from, clock: this.bumpClock(), epoch: this.epoch, view: this.snapshot() });
        break;

      case 'view-sync':
        if (msg.epoch < this.epoch) break;
        this.applySnapshot(msg.view, msg.epoch);
        break;
    }
  }

  // --------------------------------------------------------------- merge ---

  /** Public so tests/tools can force a handshake; also used internally. */
  initiateMergeWith(peerId: NodeId, now: number): void {
    if (this.state !== 'IDLE') return;
    this.state = 'PROPOSING';
    this.mergePeer = peerId;
    this.mergeRetries = 0;
    this.sendPropose(peerId, now);
    this.emit(`initiating merge handshake with ${peerId}`);
  }

  private onMergePropose(msg: Extract<Message, { type: 'merge-propose' }>, now: number): void {
    if (digestOfSnapshot(msg.view) === this.digest) return;
    if (msg.epoch < this.epoch) {
      this.sendCommitTo(msg.from);
      return;
    }
    if (this.state === 'IDLE') {
      this.becomeResponder(msg.from, now);
    } else if (this.state === 'PROPOSING') {
      // Deterministic tie-break: higher (epoch, nodeId) tuple leads.
      const theirsLeads = msg.epoch > this.epoch || (msg.epoch === this.epoch && msg.from > this.id);
      if (theirsLeads) {
        this.emit(`yielding to ${msg.from}'s merge proposal`);
        this.becomeResponder(msg.from, now);
      }
      // Otherwise ignore: the peer will yield to our proposal.
    } else if (this.state === 'RESPONDING' && this.respondPeer === msg.from) {
      this.sendAccept(msg.from);
    }
  }

  private becomeResponder(peer: NodeId, now: number): void {
    this.state = 'RESPONDING';
    this.respondPeer = peer;
    this.respondSince = now;
    this.mergePeer = null;
    this.sendAccept(peer);
  }

  private sendPropose(peer: NodeId, now: number): void {
    this.lastProposeAt = now;
    this.stats.proposesSent++;
    this.send({ type: 'merge-propose', from: this.id, to: peer, clock: this.bumpClock(), epoch: this.epoch, digest: this.digest, view: this.snapshot() });
  }

  private sendAccept(peer: NodeId): void {
    this.stats.acceptsSent++;
    this.send({ type: 'merge-accept', from: this.id, to: peer, clock: this.bumpClock(), epoch: this.epoch, digest: this.digest, view: this.snapshot() });
  }

  private broadcastCommit(): void {
    this.lastRebroadcastEpoch = this.epoch;
    for (const m of this.members.values()) {
      if (m.id !== this.id) this.sendCommitTo(m.id);
    }
  }

  private sendCommitTo(peer: NodeId): void {
    this.send({ type: 'merge-commit', from: this.id, to: peer, clock: this.bumpClock(), epoch: this.epoch, view: this.snapshot() });
  }

  private sendSyncTo(peer: NodeId): void {
    this.send({ type: 'view-sync', from: this.id, to: peer, clock: this.bumpClock(), epoch: this.epoch, view: this.snapshot() });
  }

  // -------------------------------------------------------------- helpers ---

  private onPeerDigest(peerId: NodeId, epoch: number, digest: string, now: number): void {
    if (digest === this.digest) return;
    if (epoch < this.epoch) {
      this.sendSyncTo(peerId);
    } else if (epoch > this.epoch) {
      this.send({ type: 'view-request', from: this.id, to: peerId, clock: this.bumpClock() });
    } else if (!this.members.has(peerId)) {
      this.initiateMergeWith(peerId, now);
    } else {
      this.send({ type: 'view-request', from: this.id, to: peerId, clock: this.bumpClock() });
    }
  }

  private applySnapshot(view: ViewSnapshot, epoch: number): void {
    this.members = mergeMembers(this.members, view.members);
    this.epoch = epoch;
    this.refuteSelf();
  }

  /** If the merged view marks this node non-alive, refute with a higher incarnation. */
  private refuteSelf(): void {
    const self = this.members.get(this.id);
    if (self && self.status !== 'alive') {
      this.members.set(this.id, { ...self, incarnation: self.incarnation + 1, status: 'alive' });
    }
  }

  private markSuspect(target: NodeId, now: number): void {
    const m = this.members.get(target);
    if (m && m.status === 'alive') this.setStatus(m, 'suspect', now);
  }

  private setStatus(m: MemberInfo, status: MemberStatus, now: number): void {
    m.status = status;
    m.statusChangedAt = now;
    this.emit(`member ${m.id} -> ${status}`);
  }

  private pickAlivePeer(exclude: NodeId[]): NodeId | null {
    const candidates = [...this.members.values()].filter(
      (m) => m.status === 'alive' && !exclude.includes(m.id),
    );
    if (candidates.length === 0) return null;
    return candidates[Math.floor(this.rng() * candidates.length)].id;
  }

  private pickRejoinTarget(): NodeId | null {
    const candidates = this.bootPeers.filter((p) => {
      if (p === this.id || this.pendingReqs.has(p)) return false;
      const m = this.members.get(p);
      return !m || m.status === 'dead';
    });
    if (candidates.length === 0) return null;
    return candidates[Math.floor(this.rng() * candidates.length)];
  }

  private bumpClock(): number {
    return ++this.clock;
  }

  private send(msg: Message): void {
    this.network.send(msg);
  }

  private emit(text: string): void {
    this.log?.(this.id, text);
  }
}
