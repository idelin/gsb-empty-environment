import { setImmediate } from 'node:timers/promises';
import type { ChangeEvent } from './types.ts';

/**
 * Simulates a source database emitting a large transaction's change stream
 * (e.g. one big UPDATE). Events are produced lazily one at a time, so the
 * generator itself holds O(1) memory — mirroring a real CDC cursor.
 */
export async function* generateChanges(
  count: number,
  options: { table?: string } = {},
): AsyncGenerator<ChangeEvent> {
  const table = options.table ?? 'orders';
  for (let i = 0; i < count; i++) {
    yield {
      lsn: i,
      op: 'UPDATE',
      table,
      key: `id:${i}`,
      payload: JSON.stringify({ status: 'shipped', seq: i }),
    };
    // Let the consumer drain periodically so producer/consumer interleave.
    if (i % 8192 === 8191) {
      await setImmediate();
    }
  }
}
