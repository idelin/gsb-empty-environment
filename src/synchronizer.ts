/**
 * 最终一致同步器（Synchronizer）
 *
 * 对两个副本的同名键做版本向量合并：
 *   - 一方版本支配另一方 → 快速快照覆盖（保留支配方）；
 *   - 双方版本并发       → 全部保留为同名分支（Sibling），等待上层业务解决。
 * 合并是交换、结合、幂等的：副本间反复同步最终收敛到同一状态。
 */

import { Replica, type VersionedValue } from './replica.ts';

export type MergeAction = 'fast-overwrite' | 'sibling-fork' | 'in-sync';

export interface KeyMergeReport {
  key: string;
  action: MergeAction;
  /** 合并后保留的分支数。 */
  siblingCount: number;
}

export interface SyncReport {
  from: string;
  to: string;
  keys: KeyMergeReport[];
}

/** 合并两组分支：丢弃被支配的分支，保留并发分支。 */
export function mergeSiblings(
  a: VersionedValue[],
  b: VersionedValue[],
): { siblings: VersionedValue[]; action: MergeAction } {
  const all = [...a, ...b];
  const kept = all.filter(
    (candidate) =>
      !all.some(
        (other) => other !== candidate && other.version.dominates(candidate.version),
      ),
  );
  // 去重：双方已持有完全相同的版本时只留一份。
  const unique = kept.filter(
    (candidate, i) =>
      kept.findIndex(
        (other) => other.version.compare(candidate.version) === 'equal',
      ) === i,
  );

  let action: MergeAction;
  if (unique.length > 1) {
    action = 'sibling-fork';
  } else if (a.length === 1 && b.length === 1 &&
             a[0].version.compare(b[0].version) === 'equal') {
    action = 'in-sync';
  } else {
    action = 'fast-overwrite';
  }
  return { siblings: unique, action };
}

/** 单向同步：把 source 的状态合并进 target。 */
export function syncOneWay(source: Replica, target: Replica): SyncReport {
  const report: SyncReport = { from: source.id, to: target.id, keys: [] };
  for (const [key, sourceSiblings] of source.dump()) {
    const targetSiblings = target.get(key).siblings;
    const { siblings, action } = mergeSiblings(targetSiblings, sourceSiblings);
    target.load(key, siblings);
    report.keys.push({ key, action, siblingCount: siblings.length });
  }
  return report;
}

/** 双向同步：执行后两个副本对该键集合达到一致。 */
export function syncBidirectional(a: Replica, b: Replica): [SyncReport, SyncReport] {
  const first = syncOneWay(a, b);
  const second = syncOneWay(b, a);
  return [first, second];
}
