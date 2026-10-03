/**
 * inspector.ts — 在线巡检器：持续比对源库与目标库的数据摘要。
 *
 * 单个巡检周期（patrol）分三级，成本逐级收窄：
 *   L1  比对 Merkle 根哈希            —— O(1)，一致即放行；
 *   L2  逐分区比对滚动 XOR 校验和      —— O(P)，圈定失配分区；
 *   L3  仅对失配分区做行级哈希 diff    —— 精确定位主键与差异字段。
 */
import { hashEquals, type Hash64 } from "./hash.ts";
import { hashRow, type Row, type TableStore } from "./store.ts";

export type DiffKind = "tampered" | "missing_in_target" | "missing_in_source";

export interface RowDifference {
  pk: string;
  kind: DiffKind;
  /** kind 为 tampered 时，列出取值不一致的字段名。 */
  changedFields?: string[];
  sourceRow?: Row;
  targetRow?: Row;
}

export interface PartitionMismatch {
  partition: number;
  differences: RowDifference[];
}

export interface PatrolReport {
  cycle: number;
  inSync: boolean;
  /** 本周期耗时（毫秒）。 */
  durationMs: number;
  /** 本周期实际做行级哈希比对行数（验证下钻成本足够小）。 */
  rowsCompared: number;
  mismatches: PartitionMismatch[];
}

function changedFields(a: Row, b: Row): string[] {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  const out: string[] = [];
  for (const k of keys) {
    if (JSON.stringify(a[k]) !== JSON.stringify(b[k])) out.push(k);
  }
  return out.sort();
}

export class SyncInspector {
  private cycle = 0;
  readonly source: TableStore;
  readonly target: TableStore;

  constructor(source: TableStore, target: TableStore) {
    this.source = source;
    this.target = target;
  }

  /** 执行一个巡检周期，返回比对报告。 */
  patrol(): PatrolReport {
    const started = performance.now();
    this.cycle++;
    const report: PatrolReport = {
      cycle: this.cycle,
      inSync: true,
      durationMs: 0,
      rowsCompared: 0,
      mismatches: [],
    };

    // L1：Merkle 根快速比对。
    if (this.source.index.rootHash() === this.target.index.rootHash()) {
      report.durationMs = performance.now() - started;
      return report;
    }

    // L2：分区级比对，圈定失配分区。
    const badPartitions = this.source.index.mismatchingPartitions(
      this.target.index,
    );

    // L3：失配分区行级下钻，定位主键。
    for (const p of badPartitions) {
      const differences = this.diffPartition(p, report);
      if (differences.length > 0) {
        report.mismatches.push({ partition: p, differences });
      }
    }
    report.inSync = report.mismatches.length === 0;
    report.durationMs = performance.now() - started;
    return report;
  }

  private diffPartition(p: number, report: PatrolReport): RowDifference[] {
    const sourceHashes = new Map<string, Hash64>();
    const targetHashes = new Map<string, Hash64>();
    const sourceRows = new Map<string, Row>();
    const targetRows = new Map<string, Row>();

    for (const row of this.source.rowsInPartition(p)) {
      sourceHashes.set(row.id, hashRow(row));
      sourceRows.set(row.id, row);
      report.rowsCompared++;
    }
    for (const row of this.target.rowsInPartition(p)) {
      targetHashes.set(row.id, hashRow(row));
      targetRows.set(row.id, row);
      report.rowsCompared++;
    }

    const diffs: RowDifference[] = [];
    for (const [pk, sh] of sourceHashes) {
      const th = targetHashes.get(pk);
      if (th === undefined) {
        diffs.push({
          pk,
          kind: "missing_in_target",
          sourceRow: sourceRows.get(pk),
        });
      } else if (!hashEquals(sh, th)) {
        diffs.push({
          pk,
          kind: "tampered",
          changedFields: changedFields(
            sourceRows.get(pk)!,
            targetRows.get(pk)!,
          ),
          sourceRow: sourceRows.get(pk),
          targetRow: targetRows.get(pk),
        });
      }
    }
    for (const pk of targetHashes.keys()) {
      if (!sourceHashes.has(pk)) {
        diffs.push({
          pk,
          kind: "missing_in_source",
          targetRow: targetRows.get(pk),
        });
      }
    }
    return diffs.sort((a, b) => (a.pk < b.pk ? -1 : 1));
  }
}
