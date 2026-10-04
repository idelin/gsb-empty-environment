/**
 * Stop-and-wait-free replication over an unreliable channel: cumulative
 * ACKs, sequence numbers, retransmission of unacknowledged operations
 * and idempotent application on the receiver (dedup by opId).  Tolerates
 * packet loss, duplication and reordering.
 */

import type { ChannelMessage, LossyChannel } from './faults.ts';
import type { OpType, WalRecord } from './store.ts';

export interface ReplicatedOp {
  seq: number;
  opId: string;
  type: OpType;
  cell: string;
  payload: string | null;
}

export class ReplicaSender {
  private nextSeq = 0;
  private unacked = new Map<number, ReplicatedOp>();
  private readonly sendChannel: LossyChannel;

  constructor(sendChannel: LossyChannel, ackChannel: LossyChannel) {
    this.sendChannel = sendChannel;
    ackChannel.connect((msg) => this.onMessage(msg));
  }

  private onMessage(msg: ChannelMessage): void {
    if (msg.kind !== 'ack') return;
    const upto = msg.upto as number;
    for (const seq of [...this.unacked.keys()]) {
      if (seq <= upto) this.unacked.delete(seq);
    }
  }

  /** Queue an operation for replication. */
  send(op: Omit<ReplicatedOp, 'seq'>): void {
    const rec: ReplicatedOp = { ...op, seq: this.nextSeq++ };
    this.unacked.set(rec.seq, rec);
    this.sendChannel.send({ kind: 'ops', ops: [rec] });
  }

  /** Retransmit everything not yet acknowledged. */
  flush(): void {
    if (this.unacked.size === 0) return;
    const ops = [...this.unacked.values()].sort((a, b) => a.seq - b.seq);
    this.sendChannel.send({ kind: 'ops', ops });
  }

  get pendingAcks(): number {
    return this.unacked.size;
  }
}

export class ReplicaReceiver {
  private expected = 0;
  private buffer = new Map<number, ReplicatedOp>();
  private seen = new Set<string>();
  applied = 0;
  private readonly apply: (op: ReplicatedOp) => void;
  private readonly ackChannel?: LossyChannel;

  constructor(
    channel: LossyChannel,
    apply: (op: ReplicatedOp) => void,
    ackChannel?: LossyChannel,
  ) {
    this.apply = apply;
    this.ackChannel = ackChannel;
    channel.connect((msg) => this.onMessage(msg));
  }

  private onMessage(msg: ChannelMessage): void {
    if (msg.kind !== 'ops') return;
    for (const op of msg.ops as ReplicatedOp[]) {
      if (op.seq >= this.expected && !this.buffer.has(op.seq)) {
        this.buffer.set(op.seq, op);
      }
    }
    // drain contiguous prefix
    while (this.buffer.has(this.expected)) {
      const op = this.buffer.get(this.expected)!;
      this.buffer.delete(this.expected);
      this.expected++;
      if (!this.seen.has(op.opId)) {
        this.seen.add(op.opId);
        this.apply(op);
        this.applied++;
      }
    }
    this.ackChannel?.send({ kind: 'ack', upto: this.expected - 1 });
  }

  get received(): number {
    return this.expected;
  }
}

/** Drive both channels until the sender has no unacked operations. */
export function runUntilConverged(
  sender: ReplicaSender,
  channels: LossyChannel[],
  maxRounds = 10_000,
): number {
  for (let round = 0; round < maxRounds; round++) {
    for (const ch of channels) ch.pump();
    if (sender.pendingAcks === 0) return round;
    sender.flush();
  }
  throw new Error('replication did not converge');
}
