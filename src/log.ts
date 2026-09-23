// 日志写入器与链式校验：顺序操作、事务、副作用确认、分支与拼接。
import type {
  LogRecord,
  RecordBody,
  StateOp,
  TxId,
  Verification,
  CompactedSegment,
  Checkpoint,
} from './types.ts';
import { LogCorruptionError, SpliceError } from './types.ts';
import { GENESIS, hashRecord, recordHashOf } from './hash.ts';
import { invertOp, applyOp, type State } from './ops.ts';
import { deepClone } from './hash.ts';

export class LogWriter {
  readonly records: LogRecord[] = [];
  private openTx: TxId | null = null;
  private readonly opIds = new Set<string>();
  private readonly effectIds = new Set<string>();
  private staged: LogRecord[] = [];
  private readonly view: State;

  constructor(view: State = {}) {
    this.view = view;
  }

  private tip(): string {
    return this.records.length === 0 ? GENESIS : this.records[this.records.length - 1].hash;
  }

  private push(body: RecordBody): LogRecord {
    const last = this.records[this.records.length - 1];
    const rec: LogRecord = {
      seq: last === undefined ? 1 : last.seq + 1, // 分叉日志从基点之后继续编号
      prev: this.tip(),
      body,
      hash: '',
    };
    rec.hash = hashRecord(rec.seq, rec.prev, rec.body);
    this.records.push(rec);
    return rec;
  }

  // 追加一条状态操作。opId 唯一；在事务中则暂存，提交前读者不可见。
  append(opId: string, op: StateOp, effectId: string | null = null): LogRecord {
    if (this.opIds.has(opId)) throw new Error(`重复的操作 ID: ${opId}`);
    this.opIds.add(opId);
    const inverse = invertOp(this.view, op);
    const rec = this.push({ kind: 'op', opId, op: deepClone(op), inverse, tx: this.openTx, effectId });
    if (this.openTx === null) {
      applyOp(this.view, op);
    } else {
      this.staged.push(rec);
    }
    return rec;
  }

  beginTx(tx: TxId): LogRecord {
    if (this.openTx !== null) throw new Error(`已有未结束事务: ${this.openTx}`);
    this.openTx = tx;
    return this.push({ kind: 'tx.begin', tx });
  }

  commitTx(tx: TxId): LogRecord {
    if (this.openTx !== tx) throw new Error(`没有待提交的事务: ${tx}`);
    for (const rec of this.staged) {
      if (rec.body.kind === 'op') applyOp(this.view, rec.body.op);
    }
    this.staged = [];
    this.openTx = null;
    return this.push({ kind: 'tx.commit', tx });
  }

  abortTx(tx: TxId): LogRecord {
    if (this.openTx !== tx) throw new Error(`没有待中止的事务: ${tx}`);
    this.staged = [];
    this.openTx = null;
    return this.push({ kind: 'tx.abort', tx });
  }

  // 外部副作用的确认回执：回放时凭此跳过重复触发。
  confirmEffect(effectId: string, detail = ''): LogRecord {
    if (this.effectIds.has(effectId)) throw new Error(`重复的副作用确认: ${effectId}`);
    this.effectIds.add(effectId);
    return this.push({ kind: 'effect', effectId, detail });
  }

  get length(): number {
    return this.records.length;
  }

  // 写入器的已提交视图（事务暂存不可见）。
  getState(): State {
    return deepClone(this.view);
  }
}

// 从主日志分叉：首条为 branch 记录，声明基点前缀摘要。
export function forkLog(main: LogRecord[], branchName: string, atSeq: number): LogWriter {
  if (atSeq < 0 || atSeq > main.length) throw new Error(`非法分叉点: ${atSeq}`);
  const baseHash = atSeq === 0 ? GENESIS : main[atSeq - 1].hash;
  const writer = new LogWriter();
  writer.records.push({
    seq: atSeq + 1,
    prev: baseHash,
    body: { kind: 'branch', branch: branchName, baseSeq: atSeq, baseHash },
    hash: '',
  });
  const first = writer.records[0];
  first.hash = hashRecord(first.seq, first.prev, first.body);
  return writer;
}

// 校验整条链：哈希自洽、prev 指针连续、seq 连续无缺号。
export function verifyChain(records: LogRecord[], startSeq = 1, startPrev = GENESIS): Verification {
  let prev = startPrev;
  for (let i = 0; i < records.length; i++) {
    const r = records[i];
    const expectSeq = startSeq + i;
    if (r.seq !== expectSeq) {
      return { ok: false, validPrefix: i, tipHash: prev, error: { seq: expectSeq, reason: `序号断裂：期望 ${expectSeq}，实际 ${r.seq}（缺中间操作不可跳过）` } };
    }
    if (r.prev !== prev) {
      return { ok: false, validPrefix: i, tipHash: prev, error: { seq: r.seq, reason: 'prev 指针与前缀摘要不符' } };
    }
    if (recordHashOf(r) !== r.hash) {
      return { ok: false, validPrefix: i, tipHash: prev, error: { seq: r.seq, reason: '记录哈希与内容不符（疑似篡改）' } };
    }
    prev = r.hash;
  }
  return { ok: true, validPrefix: records.length, tipHash: prev, error: null };
}

// 严格校验：损坏即抛错并定位操作序号，绝不跳过继续。
export function assertChain(records: LogRecord[], startSeq = 1, startPrev = GENESIS): void {
  const v = verifyChain(records, startSeq, startPrev);
  if (!v.ok && v.error) throw new LogCorruptionError(v.error.seq, v.error.reason);
}

// 前缀摘要不符则拒绝拼接：分支基点必须与主干对应前缀一致。
export function spliceBranch(main: LogRecord[], branch: LogRecord[]): LogRecord[] {
  if (branch.length === 0) throw new SpliceError('分支为空');
  const head = branch[0];
  if (head.body.kind !== 'branch') throw new SpliceError('分支首条必须是 branch 记录');
  const { baseSeq, baseHash } = head.body;
  if (baseSeq > main.length) throw new SpliceError(`基点越界: baseSeq=${baseSeq}`);
  const actualBase = baseSeq === 0 ? GENESIS : main[baseSeq - 1].hash;
  if (actualBase !== baseHash) {
    throw new SpliceError(`前缀摘要不符：分支声明 baseHash=${baseHash.slice(0, 12)}…，主干实际=${actualBase.slice(0, 12)}…`);
  }
  assertChain(main.slice(0, baseSeq));
  assertChain(branch, baseSeq + 1, baseHash);
  return main.slice(0, baseSeq).concat(branch);
}

// 压缩段哨兵：携带检查点摘要，作为回收后新链的链头。
export function makeAnchor(checkpoint: Checkpoint): LogRecord {
  const body = {
    kind: 'branch',
    branch: `checkpoint:${checkpoint.id}`,
    baseSeq: checkpoint.uptoSeq,
    baseHash: checkpoint.prefixHash,
  } as RecordBody;
  const rec: LogRecord = { seq: 0, prev: GENESIS, body, hash: '' };
  rec.hash = hashRecord(rec.seq, rec.prev, rec.body);
  return rec;
}

export function makeCompactedSegment(checkpoint: Checkpoint): CompactedSegment {
  return { checkpoint, anchor: makeAnchor(checkpoint) };
}
