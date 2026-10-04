import type { DiskStorage } from './storage.ts';
import type { AnalysisResult } from './recovery.ts';
import type { LogRecord, LSN, PageId, TxnId } from './types.ts';
import { NULL_LSN } from './types.ts';

export interface InspectionReport {
  name: string;
  violations: string[];
  warnings: string[];
  checks: string[];
}

export class InvariantViolationError extends Error {
  readonly report: InspectionReport;

  constructor(report: InspectionReport) {
    super(`不变式巡检失败 [${report.name}]:\n` + report.violations.map((v) => `  - ${v}`).join('\n'));
    this.name = 'InvariantViolationError';
    this.report = report;
  }
}

export interface SkewOptions {
  /** 样本量下限，低于该值跳过倾斜判定 */
  minSamples: number;
  /** 最热页的访问占比上限（严格大于才告警） */
  maxShare: number;
  /** z-score 阈值 */
  zScore: number;
}

const DEFAULT_SKEW: SkewOptions = { minSamples: 8, maxShare: 0.6, zScore: 3 };

/**
 * 形式化约束巡检与不变式断言仪：
 * 对 WAL 与恢复状态做全量离线完整性检查，主动发现
 * 悬挂引用、孤立环路与数据倾斜。
 */
export class Inspector {
  /** 检查 1：WAL 结构不变式（LSN 连续性、prevLSN 链、CLR 指针、事务闭合性） */
  inspectWalStructure(log: LogRecord[], opts: { requireClosedTxns?: boolean } = {}): InspectionReport {
    const report = this.blank('wal-structure');
    const requireClosed = opts.requireClosedTxns ?? false;
    report.checks.push('LSN 从 1 开始严格连续递增');
    log.forEach((record, i) => {
      if (record.lsn !== i + 1) {
        report.violations.push(`LSN 断裂: 位置 ${i + 1} 处 LSN=${record.lsn}`);
      }
    });

    report.checks.push('每事务 prevLSN 链连续');
    const lastByTxn = new Map<TxnId, LSN>();
    const endedTxns = new Set<TxnId>();
    const terminalTxns = new Map<TxnId, 'COMMIT' | 'ABORT'>();
    for (const record of log) {
      if (record.txnId === null) continue;
      if (endedTxns.has(record.txnId)) {
        report.violations.push(`孤立记录: 事务 ${record.txnId} 在 END 之后仍产生 ${record.type} (LSN ${record.lsn})`);
      }
      const expected = lastByTxn.get(record.txnId) ?? NULL_LSN;
      if (record.prevLSN !== expected) {
        report.violations.push(
          `prevLSN 链断裂: 事务 ${record.txnId} LSN ${record.lsn} 的 prevLSN=${record.prevLSN}, 期望 ${expected}`,
        );
      }
      lastByTxn.set(record.txnId, record.lsn);
      if (record.type === 'END') endedTxns.add(record.txnId);
      if (record.type === 'COMMIT' || record.type === 'ABORT') terminalTxns.set(record.txnId, record.type);
    }

    report.checks.push('CLR.undoNextLSN 指向同事务更早的可撤销记录');
    const byLSN = new Map<LSN, LogRecord>(log.map((r) => [r.lsn, r]));
    for (const record of log) {
      if (record.type !== 'CLR') continue;
      if (record.undoNextLSN === NULL_LSN) continue;
      const target = byLSN.get(record.undoNextLSN);
      if (target === undefined) {
        report.violations.push(`悬挂引用: CLR LSN ${record.lsn} 的 undoNextLSN=${record.undoNextLSN} 不存在`);
      } else if (target.txnId !== record.txnId) {
        report.violations.push(`CLR LSN ${record.lsn} 的 undoNextLSN 指向其他事务的记录`);
      } else if (target.lsn >= record.lsn) {
        report.violations.push(`CLR LSN ${record.lsn} 的 undoNextLSN 未指向更早的记录`);
      } else if (target.type !== 'UPDATE' && target.type !== 'CLR') {
        report.violations.push(`CLR LSN ${record.lsn} 的 undoNextLSN 指向了 ${target.type} 记录`);
      }
    }

    if (requireClosed) {
      report.checks.push('所有事务均已闭合（COMMIT/ABORT 后必有 END）');
      for (const [txnId] of terminalTxns) {
        if (!endedTxns.has(txnId)) {
          report.violations.push(`悬挂事务: ${txnId} 已终结但缺少 END 记录`);
        }
      }
      for (const txnId of lastByTxn.keys()) {
        if (!endedTxns.has(txnId)) {
          report.violations.push(`悬挂事务: ${txnId} 缺少 END 记录（泄漏）`);
        }
      }
    }
    return report;
  }

  /** 检查 2：悬挂引用（页面、检查点快照、分析结果的指针有效性） */
  inspectDanglingReferences(
    log: LogRecord[],
    storage: DiskStorage | null,
    analysis: AnalysisResult | null,
  ): InspectionReport {
    const report = this.blank('dangling-references');
    const byLSN = new Map<LSN, LogRecord>(log.map((r) => [r.lsn, r]));

    if (storage !== null) {
      report.checks.push('UPDATE/CLR 引用的页面必须存在于存储中');
      for (const record of log) {
        if ((record.type === 'UPDATE' || record.type === 'CLR') && !storage.hasPage(record.pageId)) {
          report.violations.push(`悬挂页面引用: LSN ${record.lsn} 引用了不存在的页面 ${record.pageId}`);
        }
      }
    }

    report.checks.push('检查点快照内指针必须有效');
    for (const record of log) {
      if (record.type !== 'END_CHECKPOINT') continue;
      for (const entry of record.transactions) {
        const target = byLSN.get(entry.lastLSN);
        if (entry.lastLSN !== NULL_LSN && target === undefined) {
          report.violations.push(`悬挂引用: 检查点(LSN ${record.lsn})中事务 ${entry.txnId} 的 lastLSN=${entry.lastLSN} 不存在`);
        } else if (target !== undefined && target.txnId !== entry.txnId) {
          report.violations.push(`检查点(LSN ${record.lsn})中事务 ${entry.txnId} 的 lastLSN 指向其他事务`);
        }
      }
      for (const dirty of record.dirtyPages) {
        const target = byLSN.get(dirty.recLSN);
        if (target === undefined) {
          report.violations.push(`悬挂引用: 检查点(LSN ${record.lsn})中页面 ${dirty.pageId} 的 recLSN=${dirty.recLSN} 不存在`);
        } else if ((target.type !== 'UPDATE' && target.type !== 'CLR') || target.pageId !== dirty.pageId) {
          report.violations.push(`检查点(LSN ${record.lsn})中页面 ${dirty.pageId} 的 recLSN 未指向该页的更新记录`);
        }
      }
    }

    if (analysis !== null) {
      report.checks.push('分析结果（TT/DPT）内指针必须有效');
      for (const entry of analysis.transactionTable.values()) {
        const target = byLSN.get(entry.lastLSN);
        if (target === undefined) {
          report.violations.push(`悬挂引用: 事务表 ${entry.txnId} 的 lastLSN=${entry.lastLSN} 不存在`);
        } else if (target.txnId !== entry.txnId) {
          report.violations.push(`事务表 ${entry.txnId} 的 lastLSN 指向其他事务`);
        }
      }
      for (const [pageId, recLSN] of analysis.dirtyPageTable) {
        const target = byLSN.get(recLSN);
        if (target === undefined) {
          report.violations.push(`悬挂引用: 脏页表 ${pageId} 的 recLSN=${recLSN} 不存在`);
        } else if ((target.type !== 'UPDATE' && target.type !== 'CLR') || target.pageId !== pageId) {
          report.violations.push(`脏页表 ${pageId} 的 recLSN 未指向该页的更新记录`);
        }
      }
    }
    return report;
  }

  /** 检查 3：孤立环路（prevLSN/undoNextLSN 链接图必须是 DAG） */
  inspectOrphanLoops(log: LogRecord[]): InspectionReport {
    const report = this.blank('orphan-loops');
    report.checks.push('prevLSN/undoNextLSN 链接图无环');
    const edges = new Map<LSN, LSN[]>();
    for (const record of log) {
      const targets: LSN[] = [];
      if (record.txnId !== null && record.prevLSN !== NULL_LSN) targets.push(record.prevLSN);
      if (record.type === 'CLR' && record.undoNextLSN !== NULL_LSN) targets.push(record.undoNextLSN);
      edges.set(record.lsn, targets);
    }
    const WHITE = 0, GRAY = 1, BLACK = 2;
    const color = new Map<LSN, number>();
    const visit = (node: LSN, stack: LSN[]): void => {
      color.set(node, GRAY);
      for (const next of edges.get(node) ?? []) {
        const c = color.get(next) ?? WHITE;
        if (c === GRAY) {
          const cycle = [...stack.slice(stack.indexOf(next)), next];
          report.violations.push(`孤立环路: LSN ${cycle.join(' -> ')} 构成环`);
        } else if (c === WHITE) {
          visit(next, [...stack, next]);
        }
      }
      color.set(node, BLACK);
    };
    for (const record of log) {
      if ((color.get(record.lsn) ?? WHITE) === WHITE) visit(record.lsn, [record.lsn]);
    }
    return report;
  }

  /** 检查 4：数据倾斜（页面访问分布的占比与 z-score 双重判定） */
  inspectDataSkew(log: LogRecord[], options: Partial<SkewOptions> = {}): InspectionReport {
    const opts: SkewOptions = { ...DEFAULT_SKEW, ...options };
    const report = this.blank('data-skew');
    report.checks.push(`页面访问倾斜检测 (minSamples=${opts.minSamples}, maxShare=${opts.maxShare}, z=${opts.zScore})`);
    const touches = new Map<PageId, number>();
    let total = 0;
    for (const record of log) {
      if (record.type === 'UPDATE' || record.type === 'CLR') {
        touches.set(record.pageId, (touches.get(record.pageId) ?? 0) + 1);
        total++;
      }
    }
    if (total < opts.minSamples || touches.size < 2) return report;

    const counts = [...touches.values()];
    const mean = total / counts.length;
    const variance = counts.reduce((acc, c) => acc + (c - mean) ** 2, 0) / counts.length;
    const stddev = Math.sqrt(variance);
    let hotPage: PageId | null = null;
    let hotCount = 0;
    for (const [pageId, count] of touches) {
      if (count > hotCount) {
        hotCount = count;
        hotPage = pageId;
      }
    }
    const share = hotCount / total;
    if (share > opts.maxShare) {
      report.warnings.push(
        `数据倾斜: 页面 ${hotPage} 访问占比 ${(share * 100).toFixed(1)}% 超过阈值 ${(opts.maxShare * 100).toFixed(0)}%`,
      );
    }
    if (stddev > 0 && (hotCount - mean) / stddev > opts.zScore) {
      report.warnings.push(
        `数据倾斜: 页面 ${hotPage} 的 z-score=${((hotCount - mean) / stddev).toFixed(2)} 超过 ${opts.zScore}`,
      );
    }
    return report;
  }

  /** 检查 5：恢复后不变式与无泄漏断言（TT 空、事务闭合、无丢失更新） */
  inspectRecoveredState(
    log: LogRecord[],
    disk: DiskStorage,
    analysis: AnalysisResult,
  ): InspectionReport {
    const report = this.blank('recovered-state');
    report.checks.push('恢复后事务表为空（无事务泄漏）');
    for (const entry of analysis.transactionTable.values()) {
      report.violations.push(`事务泄漏: ${entry.txnId} 恢复后仍滞留事务表 (status=${entry.status})`);
    }

    report.checks.push('磁盘 pageLSN 覆盖所有脏页（无丢失更新）');
    const maxLSNByPage = new Map<PageId, LSN>();
    for (const record of log) {
      if (record.type === 'UPDATE' || record.type === 'CLR') {
        const cur = maxLSNByPage.get(record.pageId) ?? NULL_LSN;
        if (record.lsn > cur) maxLSNByPage.set(record.pageId, record.lsn);
      }
    }
    for (const [pageId, lsn] of maxLSNByPage) {
      const page = disk.readPage(pageId);
      if (page === undefined) {
        report.violations.push(`悬挂页面引用: 页面 ${pageId} 在日志中存在但磁盘缺失`);
      } else if (page.pageLSN < lsn) {
        report.violations.push(`丢失更新: 页面 ${pageId} 的 pageLSN=${page.pageLSN} 落后于日志 LSN ${lsn}`);
      }
    }
    return report;
  }

  /** 全量巡检：汇总所有检查项 */
  inspectAll(
    log: LogRecord[],
    disk: DiskStorage,
    analysis: AnalysisResult | null,
    opts: { requireClosedTxns?: boolean; skew?: Partial<SkewOptions> } = {},
  ): InspectionReport {
    const parts = [
      this.inspectWalStructure(log, { requireClosedTxns: opts.requireClosedTxns }),
      this.inspectDanglingReferences(log, disk, analysis),
      this.inspectOrphanLoops(log),
      this.inspectDataSkew(log, opts.skew),
    ];
    if (analysis !== null) parts.push(this.inspectRecoveredState(log, disk, analysis));
    const merged = this.blank('full-inspection');
    for (const part of parts) {
      merged.violations.push(...part.violations);
      merged.warnings.push(...part.warnings);
      merged.checks.push(...part.checks.map((c) => `[${part.name}] ${c}`));
    }
    return merged;
  }

  /** 不变式断言：存在违规即抛出 InvariantViolationError */
  assertOk(report: InspectionReport): void {
    if (report.violations.length > 0) throw new InvariantViolationError(report);
  }

  private blank(name: string): InspectionReport {
    return { name, violations: [], warnings: [], checks: [] };
  }
}
