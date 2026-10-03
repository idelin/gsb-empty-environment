/**
 * 事务型持久化 KV 存储: WAL + 原子提交 + 崩溃自愈。
 * 日志 OP 载荷本身用 Protobuf wire-format 编码 (自举复用编解码器)。
 *
 * 提交协议: BEGIN -> OP* -> COMMIT -> sync -> 应用内存态。
 * 任意一步注入故障(断电/磁盘满): 内存态不变(回滚), 日志尾自愈截断。
 */

import { BlockDevice } from './device.ts';
import { InjectedFaultError } from './errors.ts';
import {
  JournalWriter,
  RecordType,
  recoverJournal,
  type RecoveryResult,
} from './journal.ts';
import { WireReader, WireWriter } from './wire.ts';
import type { ResourceTracker } from './tracker.ts';

export interface StoreOp {
  key: string;
  value?: Uint8Array; // undefined => 删除
}

function encodeOp(op: StoreOp): Uint8Array {
  const w = new WireWriter();
  w.writeString(1, op.key);
  if (op.value !== undefined) w.writeBytes(2, op.value);
  else w.writeBool(3, true);
  return w.finish();
}

function decodeOp(bytes: Uint8Array): StoreOp {
  const r = new WireReader(bytes);
  let key = '';
  let value: Uint8Array | undefined;
  while (!r.done) {
    const h = r.readFieldHeader();
    if (h.fieldNumber === 1) key = r.readString();
    else if (h.fieldNumber === 2) value = r.readBytes();
    else if (h.fieldNumber === 3) {
      r.readBool();
      value = undefined;
    } else r.skipField(h.wireType);
  }
  return { key, value };
}

/** 简单异步互斥锁: 保证并发事务串行提交。 */
class AsyncMutex {
  private chain: Promise<void> = Promise.resolve();

  async runExclusive<T>(fn: () => Promise<T> | T): Promise<T> {
    const prev = this.chain;
    let release!: () => void;
    this.chain = new Promise<void>((resolve) => (release = resolve));
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

export interface StoreRecoveryReport extends RecoveryResult {
  appliedOps: number;
}

export class DurableStore {
  private readonly state = new Map<string, Uint8Array>();
  private writer: JournalWriter;
  private txCounter = 0;
  private readonly mutex = new AsyncMutex();
  private readonly tracker?: ResourceTracker;

  private readonly device: BlockDevice;

  private constructor(
    device: BlockDevice,
    journalTail: number,
    tracker?: ResourceTracker,
  ) {
    this.device = device;
    this.writer = new JournalWriter(device, journalTail);
    this.tracker = tracker;
  }

  /** 打开全新存储 (设备应为空)。 */
  static create(device: BlockDevice, tracker?: ResourceTracker): DurableStore {
    return new DurableStore(device, 0, tracker);
  }

  /**
   * 崩溃恢复: 扫描日志, 截断撕裂尾部, 重放已提交事务,
   * 未提交事务安全回滚。返回恢复报告。
   */
  static recover(
    device: BlockDevice,
    tracker?: ResourceTracker,
  ): { store: DurableStore; report: StoreRecoveryReport } {
    const result = recoverJournal(device, device.size);
    const store = new DurableStore(device, result.tailOffset, tracker);
    let appliedOps = 0;
    if (result.checkpointPayload) {
      // 恢复快照: checkpoint 前已固化的状态
      const r = new WireReader(result.checkpointPayload);
      while (!r.done) {
        const h = r.readFieldHeader();
        if (h.fieldNumber === 1) {
          store.applyOp(decodeOp(r.readBytes()));
          appliedOps++;
        } else {
          r.skipField(h.wireType);
        }
      }
    }
    for (const tx of result.committed) {
      for (const opBytes of tx.ops) {
        store.applyOp(decodeOp(opBytes));
        appliedOps++;
      }
    }
    return {
      store,
      report: { ...result, appliedOps },
    };
  }

  private applyOp(op: StoreOp): void {
    if (op.value === undefined) this.state.delete(op.key);
    else this.state.set(op.key, op.value);
  }

  /**
   * 执行事务: 故障注入下保证原子性 —— 要么全部生效, 要么完全回滚。
   * 日志撕裂尾部在下次写入前自愈截断。
   */
  async transact(ops: StoreOp[]): Promise<void> {
    this.tracker?.acquire('transaction');
    try {
      await this.mutex.runExclusive(() => this.commitInternal(ops));
    } finally {
      this.tracker?.release('transaction');
    }
  }

  private commitInternal(ops: StoreOp[]): void {
    const txId = ++this.txCounter;
    const preTxTail = this.writer.offset;
    try {
      this.writer.append(RecordType.Begin, txId, new Uint8Array(0));
      for (const op of ops) {
        this.writer.append(RecordType.Op, txId, encodeOp(op));
      }
      this.writer.append(RecordType.Commit, txId, new Uint8Array(0));
      this.device.sync();
    } catch (err) {
      if (err instanceof InjectedFaultError) {
        // 状态安全回滚: 内存态未动, 日志尾回卷到事务前 (撕裂字节被逻辑覆盖)
        this.writer.truncateTo(preTxTail);
        throw err;
      }
      throw err;
    }
    // 提交成功才应用内存态
    for (const op of ops) this.applyOp(op);
  }

  /** 固化检查点: 快照当前状态, 日志可安全回收。 */
  checkpoint(): void {
    const txId = ++this.txCounter;
    const w = new WireWriter();
    for (const [key, value] of this.state) {
      w.writeBytes(1, encodeOp({ key, value }));
    }
    this.writer.append(RecordType.Checkpoint, txId, w.finish());
    this.device.sync();
  }

  get journalOffset(): number {
    return this.writer.offset;
  }

  get(key: string): Uint8Array | undefined {
    return this.state.get(key);
  }

  get size(): number {
    return this.state.size;
  }

  keys(): string[] {
    return [...this.state.keys()].sort();
  }
}
