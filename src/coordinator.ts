import type { Bus } from './bus.ts';
import type { EventLoop, TimerHandle } from './eventLoop.ts';
import type { Wal } from './wal.ts';
import type { Decision, Message, Vote } from './types.ts';

type TxState = 'PREPARING' | 'COMMITTING' | 'ABORTING' | 'DONE';

interface Tx {
  id: string;
  participants: string[];
  state: TxState;
  decision: Decision | null;
  votes: Map<string, Vote>;
  acked: Set<string>;
  retries: number;
  prepareTimer: TimerHandle | null;
  retryTimer: TimerHandle | null;
}

export interface CoordinatorHooks {
  /** 决议写入 WAL 之后、向参与者发布之前触发（测试用于注入崩溃）。 */
  afterDecision?: (txId: string, decision: Decision) => void;
}

export interface CoordinatorOptions {
  prepareTimeout?: number;
  retryInterval?: number;
  maxRetries?: number;
  hooks?: CoordinatorHooks;
}

/**
 * 两阶段提交协调器。
 * 状态转移全部先写 WAL：begin -> decision -> ack* -> end；
 * 重启后重放 WAL：未决事务按“推定中止”处理，已决事务向参与者查询并完成决议。
 */
export class Coordinator {
  crashed = false;
  readonly id: string;
  private bus: Bus;
  private loop: EventLoop;
  private wal: Wal;
  private txs = new Map<string, Tx>();
  private prepareTimeout: number;
  private retryInterval: number;
  private maxRetries: number;
  private hooks: CoordinatorHooks;

  constructor(id: string, bus: Bus, loop: EventLoop, wal: Wal, opts: CoordinatorOptions = {}) {
    this.id = id;
    this.bus = bus;
    this.loop = loop;
    this.wal = wal;
    this.prepareTimeout = opts.prepareTimeout ?? 10;
    this.retryInterval = opts.retryInterval ?? 5;
    this.maxRetries = opts.maxRetries ?? 3;
    this.hooks = opts.hooks ?? {};
    bus.register(id, this);
    this.recover();
  }

  // ---------- 对外入口 ----------

  begin(txId: string, participants: string[]): void {
    if (this.crashed) throw new Error('coordinator crashed');
    if (this.txs.has(txId)) throw new Error(`duplicate txId: ${txId}`);
    this.wal.append({ type: 'begin', txId, participants });
    const tx: Tx = {
      id: txId, participants: [...participants], state: 'PREPARING',
      decision: null, votes: new Map(), acked: new Set(), retries: 0,
      prepareTimer: null, retryTimer: null,
    };
    this.txs.set(txId, tx);
    for (const p of participants) this.send(p, { type: 'prepare', txId });
    tx.prepareTimer = this.loop.schedule(this.prepareTimeout, () => this.onPrepareTimeout(txId));
  }

  crash(): void {
    this.crashed = true;
    for (const tx of this.txs.values()) {
      tx.prepareTimer?.cancel();
      tx.retryTimer?.cancel();
    }
  }

  stateOf(txId: string): TxState | 'UNKNOWN' {
    return this.txs.get(txId)?.state ?? 'UNKNOWN';
  }

  receive(from: string, msg: Message): void {
    if (this.crashed) return;
    switch (msg.type) {
      case 'vote': this.onVote(from, msg.txId, msg.vote); break;
      case 'ack': this.onAck(from, msg.txId); break;
      case 'queryResponse': this.onQueryResponse(from, msg.txId, msg.state); break;
      case 'statusRequest': this.onStatusRequest(from, msg.txId); break;
    }
  }

  // ---------- 崩溃恢复 ----------

  private recover(): void {
    for (const rec of this.wal.replay()) {
      const tx = this.txs.get(rec.txId);
      switch (rec.type) {
        case 'begin':
          this.txs.set(rec.txId, {
            id: rec.txId, participants: [...rec.participants], state: 'PREPARING',
            decision: null, votes: new Map(), acked: new Set(), retries: 0,
            prepareTimer: null, retryTimer: null,
          });
          break;
        case 'decision':
          if (tx) {
            tx.decision = rec.decision;
            tx.state = rec.decision === 'COMMIT' ? 'COMMITTING' : 'ABORTING';
          }
          break;
        case 'ack':
          if (tx) tx.acked.add(rec.participant);
          break;
        case 'end':
        case 'audit':
          if (tx) tx.state = 'DONE';
          break;
      }
    }
    for (const tx of this.txs.values()) {
      if (tx.state === 'DONE') continue;
      if (tx.state === 'PREPARING') {
        this.decide(tx, 'ABORT'); // 未记录决议：推定中止，防止阻塞参与者
      } else {
        this.queryUnacked(tx); // 已决未完结：查询参与者并继续发布决议
        this.scheduleRetry(tx);
      }
    }
  }

  // ---------- 阶段一：投票 ----------

  private onVote(from: string, txId: string, vote: Vote): void {
    const tx = this.txs.get(txId);
    if (!tx || tx.state !== 'PREPARING') return; // 迟到/重复投票：忽略
    if (!tx.participants.includes(from)) return; // 非本事务参与者
    if (tx.votes.has(from)) return; // 重复投票去重
    tx.votes.set(from, vote);
    if (vote === 'NO') {
      this.decide(tx, 'ABORT');
    } else if (tx.votes.size === tx.participants.length) {
      this.decide(tx, 'COMMIT');
    }
  }

  private onPrepareTimeout(txId: string): void {
    if (this.crashed) return;
    const tx = this.txs.get(txId);
    if (tx && tx.state === 'PREPARING') this.decide(tx, 'ABORT'); // 超时未收齐投票
  }

  // ---------- 阶段二：决议发布 ----------

  private decide(tx: Tx, decision: Decision): void {
    if (tx.state !== 'PREPARING') return;
    tx.prepareTimer?.cancel();
    this.wal.append({ type: 'decision', txId: tx.id, decision });
    tx.decision = decision;
    tx.state = decision === 'COMMIT' ? 'COMMITTING' : 'ABORTING';
    this.hooks.afterDecision?.(tx.id, decision);
    for (const p of tx.participants) this.send(p, { type: 'decision', txId: tx.id, decision });
    this.scheduleRetry(tx);
  }

  private onAck(from: string, txId: string): void {
    const tx = this.txs.get(txId);
    if (!tx || tx.state === 'DONE' || tx.state === 'PREPARING') return;
    if (tx.acked.has(from)) return; // 重复ACK
    tx.acked.add(from);
    this.wal.append({ type: 'ack', txId, participant: from });
    if (tx.acked.size === tx.participants.length) this.finish(tx);
  }

  private finish(tx: Tx): void {
    tx.retryTimer?.cancel();
    this.wal.append({ type: 'end', txId: tx.id, decision: tx.decision });
    tx.state = 'DONE';
  }

  // ---------- 重试与未决审计 ----------

  private scheduleRetry(tx: Tx): void {
    tx.retryTimer?.cancel();
    tx.retryTimer = this.loop.schedule(this.retryInterval, () => this.onRetry(tx.id));
  }

  private onRetry(txId: string): void {
    if (this.crashed) return;
    const tx = this.txs.get(txId);
    if (!tx || tx.state === 'DONE' || tx.state === 'PREPARING') return;
    const pending = tx.participants.filter((p) => !tx.acked.has(p));
    if (pending.length === 0) {
      this.finish(tx);
      return;
    }
    if (tx.retries >= this.maxRetries) {
      // 重试达到上限：记录未决审计事件，事务按已决结果关闭
      this.wal.append({
        type: 'audit', event: 'PENDING_PARTICIPANTS',
        txId: tx.id, decision: tx.decision, participants: pending,
      });
      tx.state = 'DONE';
      return;
    }
    tx.retries++;
    this.queryUnacked(tx);
    this.scheduleRetry(tx);
  }

  private queryUnacked(tx: Tx): void {
    for (const p of tx.participants) {
      if (!tx.acked.has(p)) this.send(p, { type: 'query', txId: tx.id });
    }
  }

  private onQueryResponse(from: string, txId: string, state: string): void {
    const tx = this.txs.get(txId);
    if (!tx || tx.state === 'DONE' || !tx.decision) return;
    const expected = tx.decision === 'COMMIT' ? 'COMMITTED' : 'ABORTED';
    if (state === expected) {
      this.onAck(from, txId);
    } else {
      // 参与者仍处 PREPARED/UNKNOWN：重发决议
      this.send(from, { type: 'decision', txId, decision: tx.decision });
    }
  }

  /** 参与者崩溃恢复后主动询问事务结果，避免孤儿事务。 */
  private onStatusRequest(from: string, txId: string): void {
    const tx = this.txs.get(txId);
    if (tx?.decision) {
      this.send(from, { type: 'statusResponse', txId, decision: tx.decision });
    } else if (!tx) {
      this.send(from, { type: 'statusResponse', txId, decision: 'ABORT' }); // 无记录：安全中止
    }
    // PREPARING 未决：不回复，参与者稍后重试
  }

  private send(to: string, msg: Message): void {
    if (this.crashed) return;
    this.bus.send(this.id, to, msg);
  }
}
