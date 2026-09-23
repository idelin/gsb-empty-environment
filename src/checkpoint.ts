// 检查点：绑定日志前缀摘要的可信快照；支持增量恢复、压缩回收与拼接校验。
import type { LogRecord, Checkpoint, CompactedSegment } from './types.ts';
import { CheckpointError, LogCorruptionError } from './types.ts';
import { GENESIS, checkpointHash, deepClone } from './hash.ts';
import { assertChain, makeCompactedSegment } from './log.ts';
import { Interpreter, type EffectSink } from './interpreter.ts';

// 从解释器当前位置创建检查点（要求没有打开的事务，保证快照只含已提交状态）。
export function createCheckpoint(id: string, itp: Interpreter): Checkpoint {
  if (itp.hasOpenTx()) throw new CheckpointError('存在未提交事务，不能创建检查点');
  const body = {
    id,
    uptoSeq: itp.absolutePosition,
    prefixHash: itp.prefixHash(),
    state: itp.getState(),
    appliedOpIds: itp.appliedOpIdList(),
    confirmedEffects: itp.confirmedEffectList(),
    pendingEffects: itp.pendingEffectList(),
  };
  return { ...body, hash: checkpointHash(body) };
}

export function verifyCheckpoint(cp: Checkpoint): boolean {
  const { hash, ...rest } = cp;
  return checkpointHash(rest) === hash;
}

export function assertCheckpoint(cp: Checkpoint): void {
  if (!verifyCheckpoint(cp)) throw new CheckpointError(`检查点 ${cp.id} 摘要与内容不符（疑似篡改）`);
}

export interface RecoveryResult {
  interpreter: Interpreter;
  usedCheckpoint: Checkpoint | null;
}

// 逐故障点恢复：严格校验链（损坏定位到操作序号，绝不跳过），
// 再选择与前缀摘要匹配的最新检查点做增量回放；无匹配则从原点全量回放。
// 输入既可以是完整日志（首条 seq=1），也可以是压缩段尾部（首条链接到检查点摘要）。
export function recover(log: LogRecord[], checkpoints: Checkpoint[] = [], onEffect?: EffectSink): RecoveryResult {
  const valid = checkpoints.filter(verifyCheckpoint); // 篡改过的检查点直接弃用
  let seed: Checkpoint | null = null;
  let tail: LogRecord[];

  if (log.length === 0) {
    // 全量压缩：只剩检查点
    seed = valid.sort((a, b) => b.uptoSeq - a.uptoSeq)[0] ?? null;
    tail = [];
  } else if (log[0].seq === 1) {
    assertChain(log); // 完整日志：从创世校验，任何损坏在此抛出 LogCorruptionError(seq)
    const usable = valid
      .filter((cp) => {
        if (cp.uptoSeq > log.length) return false;
        const actual = cp.uptoSeq === 0 ? GENESIS : log[cp.uptoSeq - 1].hash;
        return actual === cp.prefixHash; // 前缀摘要必须匹配，否则拒绝以其为基
      })
      .sort((a, b) => b.uptoSeq - a.uptoSeq);
    seed = usable[0] ?? null;
    tail = seed ? log.slice(seed.uptoSeq) : log;
  } else {
    // 压缩段：首条必须链接到某个可信检查点的前缀摘要
    const anchors = valid
      .filter((cp) => log[0].seq === cp.uptoSeq + 1 && log[0].prev === cp.prefixHash)
      .sort((a, b) => b.uptoSeq - a.uptoSeq);
    seed = anchors[0] ?? null;
    if (seed === null) {
      // 头部缺失等价于日志损坏：定位到缺口前的操作序号，绝不跳过继续
      throw new LogCorruptionError(
        Math.max(1, log[0].seq - 1),
        `日志起点缺失：seq=${log[0].seq} 之前的前缀不存在，且无任何可信检查点可作为锚点`,
      );
    }
    assertChain(log, seed.uptoSeq + 1, seed.prefixHash);
    tail = log;
  }

  const itp = new Interpreter(tail, { seed: seed ?? undefined, onEffect });
  if (seed !== null) itp.firePending(); // 重新驱动检查点遗留的未确认副作用
  itp.runToEnd();
  return { interpreter: itp, usedCheckpoint: seed };
}

export interface CompactionResult {
  segment: CompactedSegment;
  tail: LogRecord[]; // 检查点之后的原始记录（原样保留，链式校验连续）
}

// 日志压缩：回收检查点之前的历史，以检查点哨兵作为新链头。
// 检查点与日志前缀摘要不符时拒绝回收。
export function compact(log: LogRecord[], cp: Checkpoint): CompactionResult {
  assertCheckpoint(cp);
  if (cp.uptoSeq > log.length) throw new CheckpointError('检查点超出日志长度');
  const actual = cp.uptoSeq === 0 ? GENESIS : log[cp.uptoSeq - 1].hash;
  if (actual !== cp.prefixHash) {
    throw new CheckpointError(`检查点 ${cp.id} 绑定的前缀摘要与日志不符，拒绝回收`);
  }
  const tail = log.slice(cp.uptoSeq);
  assertChain(tail, cp.uptoSeq + 1, cp.prefixHash);
  return { segment: makeCompactedSegment(cp), tail };
}

// 检查点回收：只保留 uptoSeq 最新的 retain 个（旧的已被新检查点覆盖）。
export function reclaimCheckpoints(checkpoints: Checkpoint[], retain = 1): Checkpoint[] {
  return [...checkpoints].sort((a, b) => b.uptoSeq - a.uptoSeq).slice(0, Math.max(1, retain));
}
