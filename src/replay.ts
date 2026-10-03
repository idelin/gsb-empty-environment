import type { DmlStatement } from './types.ts';
import type { InMemoryTable } from './store.ts';

/**
 * 补偿回放引擎：把冲正 DML 逐条应用到目标表。
 * 回放本身不再计入待冲正日志（使用独立的回放事务前缀）。
 */
export function replayStatements(
  tables: Map<string, InMemoryTable>,
  statements: DmlStatement[],
): { applied: number } {
  let applied = 0;
  for (const stmt of statements) {
    const table = tables.get(stmt.table);
    if (!table) throw new Error(`未知表: ${stmt.table}`);
    const txId = `replay-${stmt.sourceSequence}`;
    switch (stmt.kind) {
      case 'INSERT':
        table.insert(stmt.values ?? {}, txId);
        break;
      case 'UPDATE': {
        const where = stmt.where ?? {};
        const pk = Object.values(where)[0];
        const n = table.update((row) => row[table.pkColumn] === pk, stmt.set ?? {}, txId);
        if (n !== 1) throw new Error(`补偿 UPDATE 未命中唯一行: pk=${String(pk)}`);
        break;
      }
      case 'DELETE': {
        const where = stmt.where ?? {};
        const pk = Object.values(where)[0];
        table.delete((row) => row[table.pkColumn] === pk, txId);
        break;
      }
    }
    applied += 1;
  }
  return { applied };
}
