import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { CdcEvent, DmlStatement } from './types.ts';
import { generateCompensation, selectEvents } from './compensator.ts';
import { renderScript } from './sql-renderer.ts';

export interface RollbackManifest {
  /** 包标识：rollback-<txId 或时间窗>-<生成时间> */
  packageId: string;
  generatedAt: string;
  filter: { txId?: string; fromMs?: number; toMs?: number };
  eventCount: number;
  statementCount: number;
  /** 覆盖的 CDC sequence 区间 */
  sequenceRange: { min: number; max: number } | null;
  /** rollback.sql 的 SHA-256，用于执行前完整性校验 */
  sha256: string;
}

export interface RollbackPackage {
  manifest: RollbackManifest;
  statements: DmlStatement[];
  sql: string;
}

/** 基于 CDC 日志生成数据回滚包（内存形态） */
export function buildRollbackPackage(
  log: CdcEvent[],
  filter: { txId?: string; fromMs?: number; toMs?: number },
): RollbackPackage {
  const events = selectEvents(log, filter);
  if (events.length === 0) throw new Error('筛选条件下没有可冲正的 CDC 事件');
  const statements = generateCompensation(events);
  const sql = renderScript(statements);
  const sequences = events.map((e) => e.sequence);
  const idKey = filter.txId ?? `${filter.fromMs ?? 'min'}-${filter.toMs ?? 'max'}`;
  return {
    manifest: {
      packageId: `rollback-${idKey}-${Date.now()}`,
      generatedAt: new Date().toISOString(),
      filter,
      eventCount: events.length,
      statementCount: statements.length,
      sequenceRange: { min: Math.min(...sequences), max: Math.max(...sequences) },
      sha256: createHash('sha256').update(sql).digest('hex'),
    },
    statements,
    sql,
  };
}

/** 将回滚包落盘：manifest.json / statements.json / rollback.sql */
export function writeRollbackPackage(pkg: RollbackPackage, dir: string): void {
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'manifest.json'), JSON.stringify(pkg.manifest, null, 2));
  writeFileSync(join(dir, 'statements.json'), JSON.stringify(pkg.statements, null, 2));
  writeFileSync(join(dir, 'rollback.sql'), pkg.sql);
}
