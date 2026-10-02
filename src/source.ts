import { EventEmitter } from 'node:events';
import type { ChangeEvent, Row } from './types.ts';

/**
 * 模拟源库：接收业务 SQL，执行后异步发出 CDC 事件。
 * 分流发生在 CDC 管道上，业务调用方对归档/删除完全无感知。
 */
export class SimulatedSourceDB extends EventEmitter {
  readonly name: string;
  private readonly tables = new Map<string, Map<string | number, Row>>();

  constructor(name = 'source-db') {
    super();
    this.name = name;
  }

  seed(table: string, keyColumn: string, rows: Row[]): void {
    const store = this.tables.get(table) ?? new Map<string | number, Row>();
    for (const row of rows) {
      store.set(row[keyColumn] as string | number, { ...row });
    }
    this.tables.set(table, store);
  }

  get(table: string, id: string | number): Row | undefined {
    return this.tables.get(table)?.get(id);
  }

  /** 支持：UPDATE <table> SET col=val, ... WHERE <key> = <val> */
  executeUpdate(sql: string): number {
    const match = sql.match(
      /^\s*UPDATE\s+(\w+)\s+SET\s+(.+?)\s+WHERE\s+(\w+)\s*=\s*(['"]?)(\w+)\4\s*;?\s*$/i,
    );
    if (!match) throw new Error(`unsupported SQL: ${sql}`);
    const [, table, setClause, keyColumn, , rawKey] = match;
    const key: string | number = /^\d+$/.test(rawKey) ? Number(rawKey) : rawKey;
    const store = this.tables.get(table);
    const before = store?.get(key);
    if (!before) return 0;

    const after: Row = { ...before };
    for (const assignment of setClause.split(',')) {
      const [col, rawVal] = assignment.split('=').map((s) => s.trim());
      const val = rawVal.replace(/^['"]|['"]$/g, '');
      after[col] = /^-?\d+$/.test(val) ? Number(val) : val;
    }
    store!.set(key, after);

    const event: ChangeEvent = {
      kind: 'update',
      table,
      keyColumn,
      primaryKey: key,
      before,
      after,
      source: this.name,
      occurredAt: new Date().toISOString(),
    };
    queueMicrotask(() => this.emit('cdc', event));
    return 1;
  }
}
