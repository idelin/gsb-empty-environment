// 独立全日志解释器：与 Interpreter 不共享解释逻辑的单遍前向实现，
// 作为逐故障点恢复结果的对照基准（differential oracle）。
import type { LogRecord, StateValue } from './types.ts';
import { LogCorruptionError } from './types.ts';
import { GENESIS, recordHashOf, stateSummary } from './hash.ts';

export interface Truth {
  state: Record<string, StateValue>;
  summary: string;
  firedEffects: string[]; // 本次解释实际触发的副作用（已确认的不触发）
  position: number; // 解释到的记录数
}

export function fullLogInterpret(records: LogRecord[], startSeq = 1, startPrev = GENESIS): Truth {
  const state: Record<string, StateValue> = {};
  const applied = new Set<string>();
  const confirmed = new Set<string>();
  // 预取确认：已确认的外部动作不重复触发。
  for (const r of records) if (r.body.kind === 'effect') confirmed.add(r.body.effectId);
  const fired: string[] = [];
  let prev = startPrev;
  let openTx: string | null = null;
  const staged: LogRecord[] = [];

  const applyOne = (r: LogRecord): void => {
    if (r.body.kind !== 'op') return;
    const { op } = r.body;
    if (op.kind === 'kv.set') state[op.key] = JSON.parse(JSON.stringify(op.value));
    else {
      const cur = state[op.key];
      state[op.key] = (typeof cur === 'number' ? cur : 0) + op.delta;
    }
  };

  for (let i = 0; i < records.length; i++) {
    const r = records[i];
    if (r.seq !== startSeq + i) throw new LogCorruptionError(r.seq, `序号断裂：期望 ${startSeq + i}，实际 ${r.seq}`);
    if (r.prev !== prev) throw new LogCorruptionError(r.seq, 'prev 指针与前缀摘要不符');
    if (recordHashOf(r) !== r.hash) throw new LogCorruptionError(r.seq, '记录哈希与内容不符');
    const b = r.body;
    switch (b.kind) {
      case 'op': {
        if (applied.has(b.opId)) break; // 重复操作 ID 不重放
        applied.add(b.opId);
        if (b.tx === null) {
          applyOne(r);
          if (b.effectId !== null && !confirmed.has(b.effectId)) fired.push(b.effectId);
        } else staged.push(r);
        break;
      }
      case 'tx.begin':
        openTx = b.tx;
        break;
      case 'tx.commit': {
        for (const s of staged) {
          applyOne(s);
          if (s.body.kind === 'op' && s.body.effectId !== null && !confirmed.has(s.body.effectId)) {
            fired.push(s.body.effectId);
          }
        }
        staged.length = 0;
        openTx = null;
        break;
      }
      case 'tx.abort':
        staged.length = 0;
        openTx = null;
        break;
      case 'effect':
        confirmed.add(b.effectId);
        break;
      case 'branch':
        break;
    }
    prev = r.hash;
  }
  void openTx;
  return { state, summary: stateSummary(state), firedEffects: fired, position: records.length };
}
