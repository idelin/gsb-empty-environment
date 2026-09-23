// 测试辅助：确定性随机数、样例日志、重链与破坏注入。
import type { LogRecord, StateOp } from '../src/types.ts';
import { GENESIS, hashRecord, stateSummary, deepClone } from '../src/hash.ts';
import { LogWriter } from '../src/log.ts';
import { fullLogInterpret, type Truth } from '../src/differential.ts';
import type { State } from '../src/ops.ts';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// 重链：保留记录体，重算 seq/prev/hash（用于构造"链上合法"的重复记录等场景）。
export function rechain(records: LogRecord[]): LogRecord[] {
  const out: LogRecord[] = [];
  let prev = GENESIS;
  records.forEach((r, i) => {
    const rec: LogRecord = { seq: i + 1, prev, body: deepClone(r.body), hash: '' };
    rec.hash = hashRecord(rec.seq, rec.prev, rec.body);
    out.push(rec);
    prev = rec.hash;
  });
  return out;
}

export interface BuiltLog {
  records: LogRecord[];
  originalState: State; // 原始运行结束时的已提交状态（写入器视图）
  firedEffects: string[]; // 原始运行实际触发的外部动作
  truth: Truth; // 独立全日志解释器得出的基准
}

// 构造一个涵盖分支前提、事务提交/中止、副作用确认/未确认的样例日志。
export function buildSampleLog(): BuiltLog {
  const w = new LogWriter();
  const firedEffects: string[] = [];
  const fire = (eid: string): void => {
    firedEffects.push(eid);
  };

  w.append('op-1', { kind: 'kv.set', key: 'balance:ada', value: 100 });
  w.append('op-2', { kind: 'kv.set', key: 'balance:bob', value: 50 });

  w.beginTx('tx-1'); // 转账：提交前读者不可见
  w.append('op-3', { kind: 'kv.add', key: 'balance:ada', delta: -30 });
  w.append('op-4', { kind: 'kv.add', key: 'balance:bob', delta: 30 });
  w.commitTx('tx-1');

  w.append('op-5', { kind: 'kv.set', key: 'profile:ada', value: { tier: 'gold' } }, 'eff-email-1');
  fire('eff-email-1'); // 外部动作：发邮件
  w.confirmEffect('eff-email-1', 'smtp 250 OK'); // 确认回执落日志

  w.beginTx('tx-2'); // 被取消的事务：不落库
  w.append('op-6', { kind: 'kv.set', key: 'scratch:tmp', value: 1 });
  w.abortTx('tx-2');

  w.append('op-7', { kind: 'kv.add', key: 'points:ada', delta: 10 }, 'eff-points-1');
  fire('eff-points-1'); // 崩溃前来不及写确认 → 恢复时需重发（at-least-once）

  const records = w.records;
  return {
    records,
    originalState: w.getState(),
    firedEffects,
    truth: fullLogInterpret(records),
  };
}

// 随机日志生成：操作/事务/副作用/确认混合。
export function randomLog(rng: () => number, ops: number): LogRecord[] {
  const w = new LogWriter();
  let n = 0;
  let txOpen = false;
  let txN = 0;
  const effects: string[] = [];
  const confirmed = new Set<string>();
  const pick = (arr: string[]): string => arr[Math.floor(rng() * arr.length)];
  while (n < ops) {
    const r = rng();
    if (!txOpen && r < 0.12) {
      w.beginTx(`tx-${++txN}`);
      txOpen = true;
    } else if (txOpen && r < 0.2) {
      w.commitTx(`tx-${txN}`);
      txOpen = false;
    } else if (txOpen && r < 0.26) {
      w.abortTx(`tx-${txN}`);
      txOpen = false;
    } else if (r < 0.32 && effects.some((e) => !confirmed.has(e))) {
      const pending = effects.filter((e) => !confirmed.has(e));
      const eid = pick(pending);
      w.confirmEffect(eid, 'ack');
      confirmed.add(eid);
    } else {
      n++;
      const key = pick(['k0', 'k1', 'k2', 'k3']);
      const op: StateOp =
        rng() < 0.5
          ? { kind: 'kv.set', key, value: Math.floor(rng() * 100) }
          : { kind: 'kv.add', key, delta: Math.floor(rng() * 21) - 10 };
      let effectId: string | null = null;
      if (rng() < 0.25) {
        effectId = `eff-${effects.length}`;
        effects.push(effectId);
      }
      w.append(`op-${n}`, op, effectId);
    }
  }
  if (txOpen) w.commitTx(`tx-${txN}`);
  return w.records;
}

export function summaryOf(state: State): string {
  return stateSummary(state);
}
