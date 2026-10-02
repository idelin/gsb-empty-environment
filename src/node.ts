import { Rng } from './rng.ts';

export interface PendingPacket {
  seq: number;
  size: number;
  arrivedTick: number;
  attempts: number;
}

export interface NodeState {
  nextSeq: number;
  pending: PendingPacket[];
  completed: number[];
  timeouts: number;
  retransmissions: number;
  outbox: Array<{ tick: number; seq: number; attempt: number; lost: boolean }>;
}

export interface TimerHandle {
  (delayTicks: number, kind: 'ack' | 'timeout', seq: number, attempt: number): void;
}

/**
 * 演示组件：带超时重传的可靠发送节点。
 * 所有状态都是可序列化的纯数据；除变更自身状态外无任何副作用，
 * 发送动作仅追加到 outbox（同样属于状态，纳入快照）。
 */
export class NetworkNode {
  readonly lossProb: number;
  readonly timeoutTicks: number;
  state: NodeState = {
    nextSeq: 1,
    pending: [],
    completed: [],
    timeouts: 0,
    retransmissions: 0,
    outbox: [],
  };

  constructor(lossProb: number, timeoutTicks: number) {
    this.lossProb = lossProb;
    this.timeoutTicks = timeoutTicks;
  }

  onArrival(tick: number, size: number, rng: Rng, schedule: TimerHandle): number {
    const seq = this.state.nextSeq++;
    this.state.pending.push({ seq, size, arrivedTick: tick, attempts: 0 });
    this.transmit(tick, seq, rng, schedule);
    return seq;
  }

  onAck(tick: number, seq: number, attempt: number): void {
    const idx = this.state.pending.findIndex((p) => p.seq === seq);
    if (idx < 0) return; // 过期确认
    const pkt = this.state.pending[idx];
    if (pkt.attempts !== attempt) return; // 旧尝试的确认
    this.state.pending.splice(idx, 1);
    this.state.completed.push(seq);
  }

  /** 返回 true 表示触发了有效重传（此时钟中断会被记入日志） */
  onTimeout(tick: number, seq: number, attempt: number, rng: Rng, schedule: TimerHandle): boolean {
    const pkt = this.state.pending.find((p) => p.seq === seq);
    if (!pkt || pkt.attempts !== attempt) return false; // 已被确认，定时器失效
    this.state.timeouts++;
    this.state.retransmissions++;
    this.transmit(tick, seq, rng, schedule);
    return true;
  }

  private transmit(tick: number, seq: number, rng: Rng, schedule: TimerHandle): void {
    const pkt = this.state.pending.find((p) => p.seq === seq);
    if (!pkt) return;
    pkt.attempts++;
    const lost = rng.next() < this.lossProb;
    this.state.outbox.push({ tick, seq, attempt: pkt.attempts, lost });
    if (!lost) {
      schedule(1 + rng.nextInt(3), 'ack', seq, pkt.attempts);
    }
    schedule(this.timeoutTicks, 'timeout', seq, pkt.attempts);
  }
}
