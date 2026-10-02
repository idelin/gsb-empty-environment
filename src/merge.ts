import type { Entry } from './entry.ts';

/**
 * K 路归并迭代器：输入为若干按 Key 升序的记录序列，输出按 Key 升序、
 * 且每个 Key 仅保留 seq 最大（最新）的一条记录。
 *
 * 该原语同时服务于两条路径：
 *  - Range Scan：合并 MemTable + 各层 SSTable，得到用户可见的最新视图；
 *  - Compaction：合并相邻两层的 SSTable，物理收敛同一 Key 的多个版本。
 */
export function* mergeEntries(sources: Iterable<Entry>[]): Generator<Entry, void, unknown> {
  const iterators = sources.map((source) => source[Symbol.iterator]());
  const heads = iterators.map((iterator) => iterator.next());

  for (;;) {
    let minKey: string | undefined;
    for (const head of heads) {
      if (!head.done && (minKey === undefined || head.value.key < minKey)) {
        minKey = head.value.key;
      }
    }
    if (minKey === undefined) return;

    let newest: Entry | undefined;
    for (let i = 0; i < heads.length; i++) {
      const head = heads[i];
      if (!head.done && head.value.key === minKey) {
        if (newest === undefined || head.value.seq > newest.seq) {
          newest = head.value;
        }
        heads[i] = iterators[i].next();
      }
    }
    yield newest as Entry;
  }
}
