// 测试辅助：确定性伪随机日志生成与结果比较。

import { strict as assert } from 'node:assert';
import type { InputRecord, UpsertRecord } from '../src/types.ts';

export function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface GenOpts {
  seed: number;
  count: number;          // 目标记录数（近似）
  idPool: number;         // 事件 ID 池大小
  maxTimeStep: number;    // 时间步进上限
  latenessSpread: number; // 迟到事件相对最大时间的回退幅度
  watermarkEvery: number; // 每多少条插入一次水位线（0 = 不插入）
  watermarkLag: number;   // 水位线滞后于最大时间的量
}

const KINDS = ['A', 'B', 'C'] as const;

export function generateLog(opts: GenOpts): InputRecord[] {
  const rnd = mulberry32(opts.seed);
  const revs = new Map<string, number>();
  const lastUpsert = new Map<string, UpsertRecord>();
  const out: InputRecord[] = [];
  let maxTime = 0;

  const pickId = () => `e${Math.floor(rnd() * opts.idPool)}`;
  const pickKind = () => KINDS[Math.floor(rnd() * 3)];
  const nextTime = () => {
    maxTime += 1 + Math.floor(rnd() * opts.maxTimeStep);
    return maxTime;
  };
  const jitterTime = () => Math.max(0, maxTime - Math.floor(rnd() * opts.latenessSpread));

  for (let i = 0; i < opts.count; i++) {
    const r = rnd();
    if (opts.watermarkEvery > 0 && i > 0 && i % opts.watermarkEvery === 0) {
      out.push({ type: 'watermark', time: Math.max(0, maxTime - opts.watermarkLag) });
      continue;
    }
    if (r < 0.5) {
      // 新事件或更高修订（可改类型/时间）
      const id = pickId();
      const rev = (revs.get(id) ?? 0) + 1;
      revs.set(id, rev);
      const time = rnd() < 0.7 ? nextTime() : jitterTime();
      const rec: UpsertRecord = { type: 'upsert', id, rev, kind: pickKind(), time };
      lastUpsert.set(id, rec);
      out.push(rec);
    } else if (r < 0.6) {
      // 撤销（更高修订）
      const id = pickId();
      const cur = revs.get(id);
      if (cur !== undefined) {
        const rev = cur + 1;
        revs.set(id, rev);
        const time = lastUpsert.get(id)?.time ?? jitterTime();
        out.push({ type: 'retract', id, rev, time });
        lastUpsert.delete(id);
      }
    } else if (r < 0.68) {
      // 同 rev 同内容：幂等重复
      const rec = lastUpsert.get(pickId());
      if (rec) out.push({ ...rec });
    } else if (r < 0.74) {
      // 同 rev 不同内容：冲突
      const rec = lastUpsert.get(pickId());
      if (rec) out.push({ ...rec, kind: pickKind(), time: rec.time + 1 });
    } else if (r < 0.8) {
      // 过期修订
      const id = pickId();
      const cur = revs.get(id);
      if (cur !== undefined && cur > 1) {
        out.push({ type: 'upsert', id, rev: cur - 1, kind: pickKind(), time: jitterTime() });
      }
    } else {
      // 同 tick 突发事件簇
      const t = nextTime();
      const n = 1 + Math.floor(rnd() * 4);
      for (let k = 0; k < n; k++) {
        const id = pickId();
        const rev = (revs.get(id) ?? 0) + 1;
        revs.set(id, rev);
        const rec: UpsertRecord = { type: 'upsert', id, rev, kind: pickKind(), time: t };
        lastUpsert.set(id, rec);
        out.push(rec);
      }
    }
  }
  return out;
}

export function alertPairs(alerts: Map<string, { aId: string; bId: string }>): string[] {
  return [...alerts.entries()]
    .map(([id, v]) => `${id}=${v.aId}->${v.bId}`)
    .sort();
}

export function assertSameAlerts(
  actual: Map<string, { aId: string; bId: string }>,
  expected: Map<string, { aId: string; bId: string }>,
  message: string,
): void {
  assert.deepStrictEqual(alertPairs(actual), alertPairs(expected), message);
}

export function shuffle<T>(arr: T[], seed: number): T[] {
  const rnd = mulberry32(seed);
  const out = [...arr];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rnd() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}
