import type { Bus } from './bus.ts';
import type { EventLoop, TimerHandle } from './eventLoop.ts';
import type { Wal } from './wal.ts';
import type { Decision, Message, ParticipantState, Vote } from './types.ts';

export interface ParticipantOptions {
  /** 投票策略，默认全部 YES。 */
  vote?: (txId: string) => Vote;
  coordinatorId?: string;
  retryInterval?: number;
  maxRetries?: number;
}

/**
 * 2PC 参与者。本地状态同样写 WAL（prepared/committed/aborted）；
 * 崩溃重启后重放日志，对仍处于 PREPARED 的事务主动向协调器询问结果，避免孤儿事务占用资源。
 */
export class Participant {
  crashed = false;
  readonly id: string;
  private bus: Bus;
  private loop: EventLoop;
  private wal: Wal;
  private states = new Map<string, ParticipantState>();
  private retryTimers = new Map<string, TimerHandle>();
  private voteFn: (txId: string) => Vote;
  private coordinatorId: string;
  private retryInterval: number;
  private maxRetries: number;

  constructor(id: string, bus: Bus, loop: EventLoop, wal: Wal, opts: ParticipantOptions = {}) {
    this.id = id;
    this.bus = bus;
    this.loop = loop;
    this.wal = wal;
    this.voteFn = opts.vote ?? (() => 'YES');
    this.coordinatorId = opts.coordinatorId ?? 'coordinator';
    this.retryInterval = opts.retryInterval ?? 5;
    this.maxRetries = opts.maxRetries ?? 5;
    bus.register(id, this);
    this.recover();
  }

  stateOf(txId: string): ParticipantState {
    return this.states.get(txId) ?? 'UNKNOWN';
  }

  crash(): void {
    this.crashed = true;
    for (const t of this.retryTimers.values()) t.cancel();
    this.retryTimers.clear();
  }

  receive(from: string, msg: Message): void {
    if (this.crashed) return;
    switch (msg.type) {
      case 'prepare': this.onPrepare(from, msg.txId); break;
      case 'decision': this.onDecision(from, msg.txId, msg.decision); break;
      case 'query':
        this.send(from, { type: 'queryResponse', txId: msg.txId, state: this.stateOf(msg.txId) });
        break;
      case 'statusResponse': this.onStatusResponse(msg.txId, msg.decision); break;
    }
  }

  // ---------- 消息处理 ----------

  private onPrepare(from: string, txId: string): void {
    const st = this.states.get(txId);
    if (st) {
      // 重复 Prepare：重发原投票，保证幂等
      this.send(from, { type: 'vote', txId, vote: st === 'ABORTED' ? 'NO' : 'YES' });
      return;
    }
    const vote = this.voteFn(txId);
    if (vote === 'YES') {
      this.wal.append({ type: 'prepared', txId });
      this.states.set(txId, 'PREPARED');
    } else {
      // 投 NO 的参与者可单方面中止
      this.wal.append({ type: 'aborted', txId, reason: 'VOTE_NO' });
      this.states.set(txId, 'ABORTED');
    }
    this.send(from, { type: 'vote', txId, vote });
  }

  private onDecision(from: string, txId: string, decision: Decision): void {
    const st = this.states.get(txId);
    if (st === 'COMMITTED' || st === 'ABORTED') {
      this.send(from, { type: 'ack', txId }); // 重复决议：幂等确认
      return;
    }
    if (!st && decision === 'COMMIT') {
      this.wal.append({ type: 'audit', event: 'COMMIT_WITHOUT_PREPARE', txId });
      return;
    }
    this.applyDecision(txId, decision);
    this.send(from, { type: 'ack', txId });
  }

  private onStatusResponse(txId: string, decision: Decision): void {
    if (this.states.get(txId) !== 'PREPARED') return;
    this.applyDecision(txId, decision);
  }

  private applyDecision(txId: string, decision: Decision): void {
    this.wal.append({ type: decision === 'COMMIT' ? 'committed' : 'aborted', txId });
    this.states.set(txId, decision === 'COMMIT' ? 'COMMITTED' : 'ABORTED');
    this.retryTimers.get(txId)?.cancel();
    this.retryTimers.delete(txId);
  }

  // ---------- 崩溃恢复：主动追问，消除孤儿事务 ----------

  private recover(): void {
    for (const rec of this.wal.replay()) {
      if (rec.type === 'prepared') this.states.set(rec.txId, 'PREPARED');
      else if (rec.type === 'committed') this.states.set(rec.txId, 'COMMITTED');
      else if (rec.type === 'aborted') this.states.set(rec.txId, 'ABORTED');
    }
    for (const [txId, st] of this.states) {
      if (st === 'PREPARED') this.askCoordinator(txId, 0);
    }
  }

  private askCoordinator(txId: string, attempt: number): void {
    if (this.crashed || this.states.get(txId) !== 'PREPARED') return;
    if (attempt > this.maxRetries) {
      this.wal.append({ type: 'audit', event: 'ORPHAN_RISK', txId });
      return;
    }
    this.send(this.coordinatorId, { type: 'statusRequest', txId });
    this.retryTimers.set(
      txId,
      this.loop.schedule(this.retryInterval, () => this.askCoordinator(txId, attempt + 1)),
    );
  }

  private send(to: string, msg: Message): void {
    if (this.crashed) return;
    this.bus.send(this.id, to, msg);
  }
}
