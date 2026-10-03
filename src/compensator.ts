import type { CdcEvent, DmlStatement, RowImage } from './types.ts';

/**
 * 反向补偿生成器：读取 CDC 事件流，逆向生成冲正 DML 序列。
 *
 * 映射规则：
 *  - 原 INSERT -> 补偿 DELETE（按主键删除误插入的行）
 *  - 原 DELETE -> 补偿 INSERT（用 Before Image 重建被删行）
 *  - 原 UPDATE -> 补偿 UPDATE（用 Before Image 将被改列恢复为原值）
 *
 * 事件按 sequence 逆序回放，保证同一行多次变更时按 LIFO 逐层撤销。
 */
export function generateCompensation(events: CdcEvent[]): DmlStatement[] {
  const ordered = [...events].sort((a, b) => b.sequence - a.sequence);
  return ordered.map((event) => {
    switch (event.op) {
      case 'INSERT':
        return {
          kind: 'DELETE',
          table: event.table,
          where: { ...event.primaryKey },
          sourceSequence: event.sequence,
        };
      case 'DELETE':
        if (!event.before) throw new Error(`DELETE 事件缺少 Before Image: seq=${event.sequence}`);
        return {
          kind: 'INSERT',
          table: event.table,
          values: { ...event.before },
          sourceSequence: event.sequence,
        };
      case 'UPDATE': {
        if (!event.before || !event.after) {
          throw new Error(`UPDATE 事件缺少镜像: seq=${event.sequence}`);
        }
        // 只回滚真正发生变化的列，避免无谓写放大
        const revertSet: RowImage = {};
        for (const [column, afterValue] of Object.entries(event.after)) {
          if (!Object.is(event.before[column], afterValue)) {
            revertSet[column] = event.before[column];
          }
        }
        return {
          kind: 'UPDATE',
          table: event.table,
          set: revertSet,
          where: { ...event.primaryKey },
          sourceSequence: event.sequence,
        };
      }
    }
  });
}

/** 从事务日志中筛选待冲正事件：按 txId 或毫秒时间窗口 */
export function selectEvents(
  log: CdcEvent[],
  filter: { txId?: string; fromMs?: number; toMs?: number },
): CdcEvent[] {
  return log.filter((event) => {
    if (filter.txId !== undefined && event.txId !== filter.txId) return false;
    if (filter.fromMs !== undefined && event.timestampMs < filter.fromMs) return false;
    if (filter.toMs !== undefined && event.timestampMs > filter.toMs) return false;
    return true;
  });
}
