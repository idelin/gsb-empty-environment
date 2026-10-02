import type { ChangeEvent, Row } from './events.ts';

export type ChangeListener = (event: ChangeEvent) => Promise<void> | void;

/**
 * 模拟源库：接收业务 SQL 写操作并发出 CDC 变更事件。
 * 真实场景中由 binlog/WAL 订阅器扮演该角色。
 */
export class SourceDb {
  private tables = new Map<string, Map<unknown, Row>>();
  private listeners: ChangeListener[] = [];

  onChange(listener: ChangeListener): void {
    this.listeners.push(listener);
  }

  insert(table: string, key: unknown, row: Row): void {
    this.table(table).set(key, { ...row });
    void this.emit({ kind: 'insert', table, key, after: { ...row }, ts: Date.now() });
  }

  /** 对应 SQL: UPDATE <table> SET <set> WHERE id = <key> */
  async update(table: string, key: unknown, set: Row): Promise<void> {
    const before = this.table(table).get(key);
    if (!before) throw new Error(`row not found: ${table}#${String(key)}`);
    const after = { ...before, ...set };
    this.table(table).set(key, after);
    await this.emit({ kind: 'update', table, key, before: { ...before }, after: { ...after }, ts: Date.now() });
  }

  get(table: string, key: unknown): Row | undefined {
    const row = this.table(table).get(key);
    return row ? { ...row } : undefined;
  }

  private table(name: string): Map<unknown, Row> {
    let t = this.tables.get(name);
    if (!t) {
      t = new Map();
      this.tables.set(name, t);
    }
    return t;
  }

  private async emit(event: ChangeEvent): Promise<void> {
    for (const listener of this.listeners) {
      await listener(event);
    }
  }
}
