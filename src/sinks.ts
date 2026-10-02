import type { Row, Sink } from './types.ts';

export interface SinkCall {
  op: 'insert' | 'delete';
  table: string;
  record?: Row;
  id?: string | number;
}

/** 内存 Sink：记录全部调用，供测试断言与样例演示；可注入故障验证错误隔离 */
export class InMemorySink implements Sink {
  readonly name: string;
  readonly calls: SinkCall[] = [];
  failNext = false;

  constructor(name: string) {
    this.name = name;
  }

  private guard(): void {
    if (this.failNext) {
      this.failNext = false;
      throw new Error(`sink ${this.name} injected failure`);
    }
  }

  async insert(table: string, record: Row): Promise<void> {
    this.guard();
    this.calls.push({ op: 'insert', table, record });
  }

  async delete(table: string, id: string | number): Promise<void> {
    this.guard();
    this.calls.push({ op: 'delete', table, id });
  }
}

/** 控制台 Sink：样例中模拟真实异构下游（打印即代表已发送指令） */
export class ConsoleSink implements Sink {
  readonly name: string;
  private readonly label: string;

  constructor(name: string, label: string) {
    this.name = name;
    this.label = label;
  }

  async insert(table: string, record: Row): Promise<void> {
    console.log(`  [${this.label}] INSERT INTO ${table}:`, JSON.stringify(record));
  }

  async delete(table: string, id: string | number): Promise<void> {
    console.log(`  [${this.label}] DELETE ${table} WHERE id = ${id}`);
  }
}
