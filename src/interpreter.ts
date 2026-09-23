// 解释器：把操作日志回放为状态。支持逐步前进、反向步进（逆补丁）、取消事务。
// 读者只能看到已提交状态；事务暂存区对外不可见。
import type { LogRecord, Checkpoint, StateValue } from './types.ts';
import { LogCorruptionError } from './types.ts';
import { GENESIS, recordHashOf, stateSummary, deepClone } from './hash.ts';
import { applyOp, applyInverse, type State } from './ops.ts';

export type EffectSink = (effectId: string, opId: string) => void;

export interface InterpreterOptions {
  seed?: Checkpoint; // 从检查点起步（增量恢复）
  onEffect?: EffectSink; // 触发外部动作的回调；已确认的副作用不会再次触发
}

interface JournalEntry {
  rec: LogRecord;
  stagedOps: LogRecord[]; // 本记录加入暂存区的操作
  committedOps: LogRecord[]; // 本记录提交落库 / 中止时清掉的操作
  firedEffects: string[]; // 本条记录触发的副作用
  registeredConfirm: string | null; // 本会话新登记的副作用确认
  duplicate: boolean; // 重复操作 ID，未应用
}

export class Interpreter {
  private readonly records: LogRecord[];
  private readonly onEffect: EffectSink;
  private readonly baseSeq: number;

  private pos = 0;
  private tipHash: string;
  private readonly state: State = {};
  private readonly appliedOpIds = new Set<string>();
  private readonly confirmedEffects: Set<string>;
  private readonly prefetchedEffects: Set<string>;
  private readonly pendingEffects = new Set<string>(); // 已触发未确认，恢复时需重新驱动
  private openTx: string | null = null;
  private staged: LogRecord[] = [];
  private readonly journal: JournalEntry[] = [];

  constructor(records: LogRecord[], opts: InterpreterOptions = {}) {
    this.records = records;
    this.onEffect = opts.onEffect ?? (() => {});
    if (opts.seed) {
      this.baseSeq = opts.seed.uptoSeq;
      this.tipHash = opts.seed.prefixHash;
      this.state = deepClone(opts.seed.state);
      for (const id of opts.seed.appliedOpIds) this.appliedOpIds.add(id);
      this.confirmedEffects = new Set(opts.seed.confirmedEffects);
    } else {
      this.baseSeq = 0;
      this.tipHash = GENESIS;
      this.confirmedEffects = new Set();
    }
    // 预取副作用确认：已确认的外部动作在回放中绝不重复触发。
    this.prefetchedEffects = new Set(
      records.filter((r) => r.body.kind === 'effect').map((r) => (r.body as { effectId: string }).effectId),
    );
    for (const id of this.prefetchedEffects) this.confirmedEffects.add(id);
    for (const id of opts.seed?.pendingEffects ?? []) {
      if (!this.confirmedEffects.has(id)) this.pendingEffects.add(id);
    }
  }

  get position(): number {
    return this.pos;
  }
  get absolutePosition(): number {
    return this.baseSeq + this.pos;
  }
  prefixHash(): string {
    return this.tipHash;
  }
  hasOpenTx(): boolean {
    return this.openTx !== null;
  }

  // 读者视图：仅已提交状态，事务中间态不可见。
  getState(): Record<string, StateValue> {
    return deepClone(this.state);
  }

  summary(): string {
    return stateSummary(this.state);
  }

  isConfirmed(effectId: string): boolean {
    return this.confirmedEffects.has(effectId);
  }

  appliedOpIdList(): string[] {
    return [...this.appliedOpIds].sort();
  }

  confirmedEffectList(): string[] {
    return [...this.confirmedEffects].sort();
  }

  pendingEffectList(): string[] {
    return [...this.pendingEffects].sort();
  }

  // 对检查点遗留下来的未确认副作用重新驱动外部动作（at-least-once）。
  // 调用时机：以检查点为基点的恢复开始回放之前。返回本次触发的副作用。
  firePending(): string[] {
    const fired: string[] = [];
    for (const eid of this.pendingEffects) {
      if (!this.confirmedEffects.has(eid)) {
        this.onEffect(eid, 'pending');
        fired.push(eid);
      }
    }
    return fired;
  }

  private applyForward(rec: LogRecord, entry: JournalEntry): void {
    const body = rec.body;
    switch (body.kind) {
      case 'op': {
        if (this.appliedOpIds.has(body.opId)) {
          entry.duplicate = true; // 重复操作 ID：不重放第二次
          return;
        }
        if (body.tx === null && this.openTx !== null) {
          throw new LogCorruptionError(rec.seq, '事务打开期间出现事务外操作');
        }
        if (body.tx !== null && body.tx !== this.openTx) {
          throw new LogCorruptionError(rec.seq, `操作属于未打开的事务: ${body.tx}`);
        }
        this.appliedOpIds.add(body.opId);
        if (body.tx === null) {
          applyOp(this.state, body.op);
          if (body.effectId !== null && !this.confirmedEffects.has(body.effectId)) {
            entry.firedEffects.push(body.effectId);
            this.pendingEffects.add(body.effectId);
            this.onEffect(body.effectId, body.opId);
          }
        } else {
          this.staged.push(rec);
          entry.stagedOps.push(rec);
        }
        return;
      }
      case 'tx.begin': {
        if (this.openTx !== null) throw new LogCorruptionError(rec.seq, `事务嵌套: ${this.openTx} 未结束又开 ${body.tx}`);
        this.openTx = body.tx;
        return;
      }
      case 'tx.commit': {
        if (this.openTx !== body.tx) throw new LogCorruptionError(rec.seq, `提交不匹配的事务: ${body.tx}`);
        for (const opRec of this.staged) {
          if (opRec.body.kind !== 'op') continue;
          applyOp(this.state, opRec.body.op);
          const eid = opRec.body.effectId;
          if (eid !== null && !this.confirmedEffects.has(eid)) {
            entry.firedEffects.push(eid);
            this.pendingEffects.add(eid);
            this.onEffect(eid, opRec.body.opId);
          }
        }
        entry.committedOps = this.staged;
        this.staged = [];
        this.openTx = null;
        return;
      }
      case 'tx.abort': {
        if (this.openTx !== body.tx) throw new LogCorruptionError(rec.seq, `中止不匹配的事务: ${body.tx}`);
        entry.committedOps = this.staged; // 记录被清掉的暂存，供回退恢复
        this.staged = [];
        this.openTx = null;
        return;
      }
      case 'effect': {
        if (!this.confirmedEffects.has(body.effectId)) {
          this.confirmedEffects.add(body.effectId);
          entry.registeredConfirm = body.effectId;
        }
        this.pendingEffects.delete(body.effectId);
        return;
      }
      case 'branch': {
        if (body.baseSeq !== rec.seq - 1) {
          throw new LogCorruptionError(rec.seq, `分支基点与位置不符: baseSeq=${body.baseSeq}`);
        }
        if (body.baseHash !== rec.prev) {
          throw new LogCorruptionError(rec.seq, '分支基点摘要与前缀不符');
        }
        return;
      }
    }
  }

  // 前进一步；损坏即抛 LogCorruptionError（定位操作序号），绝不跳过继续。
  step(): LogRecord | null {
    if (this.pos >= this.records.length) return null;
    const rec = this.records[this.pos];
    const expectSeq = this.baseSeq + this.pos + 1;
    if (rec.seq !== expectSeq) {
      throw new LogCorruptionError(expectSeq, `序号断裂：期望 ${expectSeq}，实际 ${rec.seq}（缺中间操作不可跳过）`);
    }
    if (rec.prev !== this.tipHash) {
      throw new LogCorruptionError(rec.seq, 'prev 指针与前缀摘要不符');
    }
    if (recordHashOf(rec) !== rec.hash) {
      throw new LogCorruptionError(rec.seq, '记录哈希与内容不符（疑似篡改）');
    }
    const entry: JournalEntry = {
      rec,
      stagedOps: [],
      committedOps: [],
      firedEffects: [],
      registeredConfirm: null,
      duplicate: false,
    };
    this.applyForward(rec, entry);
    this.journal.push(entry);
    this.pos++;
    this.tipHash = rec.hash;
    return rec;
  }

  runToEnd(): void {
    while (this.step() !== null) {}
  }

  // 反向步进：用记录内携带的逆补丁精确撤销。
  stepBack(): LogRecord | null {
    const entry = this.journal.pop();
    if (!entry) return null;
    const rec = entry.rec;
    const body = rec.body;
    switch (body.kind) {
      case 'op': {
        if (!entry.duplicate) {
          this.appliedOpIds.delete(body.opId);
          if (body.tx === null) {
            applyInverse(this.state, body.inverse);
          } else {
            this.staged.pop();
          }
        }
        break;
      }
      case 'tx.begin':
        this.openTx = null;
        break;
      case 'tx.commit': {
        for (let i = entry.committedOps.length - 1; i >= 0; i--) {
          const opRec = entry.committedOps[i];
          if (opRec.body.kind === 'op') applyInverse(this.state, opRec.body.inverse);
        }
        this.staged = entry.committedOps;
        this.openTx = body.tx;
        break;
      }
      case 'tx.abort':
        this.staged = entry.committedOps;
        this.openTx = body.tx;
        break;
      case 'effect':
        if (entry.registeredConfirm !== null && !this.prefetchedEffects.has(entry.registeredConfirm)) {
          this.confirmedEffects.delete(entry.registeredConfirm);
          this.pendingEffects.add(entry.registeredConfirm); // 与正向 delete 对称
        }
        break;
      case 'branch':
        break;
    }
    this.pos--;
    this.tipHash = rec.prev;
    return rec;
  }

  // 取消：丢弃未提交的事务暂存（等价于本地 abort，不产生日志记录）。
  cancel(): boolean {
    if (this.openTx === null) return false;
    this.staged = [];
    this.openTx = null;
    return true;
  }
}
