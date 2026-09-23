import { merge, tick, type VectorClock } from './version.ts';
import type { Batch, Op, Scalar, Schema } from './ops.ts';
import { Store, type AppliedEvent } from './store.ts';
import type { MessageBus } from './bus.ts';

/** 事务句柄：缓冲操作，commit 前对外不可见。 */
export class Transaction {
  readonly ops: Op[] = [];
  private readonly replica: Replica;
  readonly txnId: string;

  constructor(replica: Replica, txnId: string) {
    this.replica = replica;
    this.txnId = txnId;
  }

  set(field: string, value: Scalar): this {
    this.ops.push(this.replica.forgeOp(this.txnId, field, value));
    return this;
  }

  delete(field: string): this {
    return this.set(field, null);
  }
}

/**
 * 副本：本地提交 -> 原子应用 -> 广播；接收 -> 幂等应用。
 * 本地回声经 opId 去重，不会二次应用、不重复触发副作用。
 */
export class Replica {
  readonly store: Store;
  private clock: VectorClock = {};
  private lamport = 0;
  private opCounter = 0;
  private sideEffects: ((e: AppliedEvent) => void)[] = [];
  readonly id: string;
  private readonly bus: MessageBus;

  constructor(id: string, schema: Schema, bus: MessageBus) {
    this.id = id;
    this.bus = bus;
    this.store = new Store(schema);
    this.store.onApply((e) => {
      // 因果版本推进：合并远端时钟
      this.clock = merge(this.clock, e.op.clock);
      this.lamport = Math.max(this.lamport, e.op.lamport);
      for (const fn of this.sideEffects) fn(e);
    });
    this.bus.subscribe(id, (batch) => this.receive(batch));
  }

  forgeOp(txnId: string, field: string, value: Scalar): Op {
    // 每个操作都推进因果版本与 Lamport 时钟，保证版本唯一且单调
    this.clock = tick(this.clock, this.id);
    this.lamport += 1;
    return {
      opId: `${this.id}:${++this.opCounter}`,
      txnId,
      replicaId: this.id,
      lamport: this.lamport,
      clock: { ...this.clock },
      field,
      value,
    };
  }

  /** 单操作快捷方式：等价于单操作事务。 */
  set(field: string, value: Scalar): void {
    this.transaction((tx) => tx.set(field, value));
  }

  /**
   * 事务：回调期间所有写入只进缓冲区；
   * commit 时作为一个批次原子应用并广播，读者看不到中间态。
   */
  transaction(fn: (tx: Transaction) => void): Batch {
    const txnId = `${this.id}:txn:${this.opCounter + 1}`;
    const tx = new Transaction(this, txnId);
    fn(tx);
    const batch: Batch = { txnId, replicaId: this.id, ops: tx.ops };
    if (tx.ops.length > 0) {
      this.store.apply(batch, 'local');
      this.bus.send(batch);
    }
    return batch;
  }

  private receive(batch: Batch): void {
    this.store.apply(batch, 'remote'); // 本地回声 / 重复消息在此被 opId 去重
  }

  /** 断线重连：从总线拉取其他副本的保留日志重放（幂等，不双应用）。 */
  syncFrom(peerIds: string[]): void {
    for (const peer of peerIds) {
      if (peer === this.id) continue;
      for (const batch of this.bus.fetchRetained(peer)) {
        this.store.apply(batch, 'remote');
      }
    }
  }

  /** 注册副作用：仅在操作首次应用时触发（本地回声不重复触发）。 */
  onSideEffect(fn: (e: AppliedEvent) => void): void {
    this.sideEffects.push(fn);
  }

  snapshot(): Readonly<Record<string, unknown>> {
    return this.store.snapshot();
  }
}
