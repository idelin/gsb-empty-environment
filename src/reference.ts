// 一次性参考计算：对同一输入日志，先做与流式实现完全一致的接收判定
// （修订解析、冲突/幂等、水位线迟到与封闭区拒绝），再把存活事件按
// (time, id) 排序，单趟算出所有满足模式的 (AID, BID) 对。用于与流式结果对拍。

import { alertIdOf } from './types.ts';
import type { InputRecord, Kind } from './types.ts';

interface FinalEvent {
  id: string;
  rev: number;
  kind: Kind | null;
  time: number;
}

export interface ReferenceResult {
  alerts: Map<string, { aId: string; bId: string }>;
  stats: {
    accepted: number;
    rejectedLate: number;
    staleRevisions: number;
    idempotentDuplicates: number;
    conflicts: number;
  };
}

function compareId(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function referenceAlerts(
  log: InputRecord[],
  windowSize: number,
  allowedLateness: number,
): ReferenceResult {
  // 1) 接收判定（到达顺序，规则与 PatternEngine 逐条对应；一次性运行无需裁剪）。
  const byId = new Map<string, FinalEvent>();
  let watermark = Number.NEGATIVE_INFINITY;
  const stats = {
    accepted: 0,
    rejectedLate: 0,
    staleRevisions: 0,
    idempotentDuplicates: 0,
    conflicts: 0,
  };

  for (const rec of log) {
    if (rec.type === 'watermark') {
      if (rec.time > watermark) watermark = rec.time;
      continue;
    }
    if (rec.time < watermark - allowedLateness) {
      stats.rejectedLate++;
      continue;
    }
    const existing = byId.get(rec.id);
    if (existing && existing.time < watermark - allowedLateness) {
      stats.rejectedLate++; // 目标事件已超出允许迟到范围：拒绝
      continue;
    }
    if (existing) {
      if (rec.rev < existing.rev) {
        stats.staleRevisions++;
        continue;
      }
      if (rec.rev === existing.rev) {
        const sameContent =
          rec.type === 'upsert'
            ? existing.kind === rec.kind && existing.time === rec.time
            : existing.kind === null;
        if (sameContent) stats.idempotentDuplicates++;
        else stats.conflicts++;
        continue;
      }
    }
    stats.accepted++;
    byId.set(rec.id, {
      id: rec.id,
      rev: rec.rev,
      kind: rec.type === 'upsert' ? rec.kind : null,
      time: rec.time,
    });
  }

  // 2) 按事件时间（同时间按事件 ID）排序，单趟计算全部匹配对。
  const events = [...byId.values()]
    .filter((e) => e.kind !== null)
    .sort((x, y) => x.time - y.time || compareId(x.id, y.id));

  const aEvents = events.filter((e) => e.kind === 'A');
  const bEvents = events.filter((e) => e.kind === 'B');
  const cTimes = events.filter((e) => e.kind === 'C').map((e) => e.time);

  const alerts = new Map<string, { aId: string; bId: string }>();
  let cPtr = 0;
  for (const a of aEvents) {
    const upper = a.time + windowSize;
    while (cPtr < cTimes.length && cTimes[cPtr] < a.time) cPtr++;
    const c0 = cPtr < cTimes.length ? cTimes[cPtr] : undefined;
    for (const b of bEvents) {
      if (b.time < a.time || b.time > upper) continue;
      if (c0 !== undefined && c0 <= b.time) continue; // C ∈ [A, B]（含端点）否决
      alerts.set(alertIdOf(a.id, b.id), { aId: a.id, bId: b.id });
    }
  }
  return { alerts, stats };
}
