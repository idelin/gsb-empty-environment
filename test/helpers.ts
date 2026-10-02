import type { ChangeRow } from '../src/types.ts';

/** 流式行源：模拟源库一次大更新产生的海量变更，逐行产出、不驻留内存 */
export async function* generateRows(count: number, table = 'orders'): AsyncGenerator<ChangeRow> {
  for (let i = 0; i < count; i++) {
    yield {
      op: 'UPDATE',
      table,
      pk: i,
      data: { status: 'SHIPPED', updated_at: '2026-10-02T00:00:00Z', version: i },
    };
  }
}

export const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, ms));
