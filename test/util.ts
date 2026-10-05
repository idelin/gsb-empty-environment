import type { Differ, DiffCursor, DiffItem, DiffPage, ScanControl } from '../src/index.ts';

/** Deterministic PRNG so every test run is reproducible. */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface CollectResult<K, V> {
  items: DiffItem<K, V>[];
  pages: DiffPage<K, V>[];
}

/**
 * Drives a diff to completion page by page. When a page is cancelled
 * before making any progress, the next page is fetched without a control
 * (mirrors a caller that retries after a cancellation).
 */
export function collectAll<K, V>(
  differ: Differ<K, V>,
  start: DiffCursor<K, V>,
  pageSize: number,
  controlFor?: (pageIndex: number) => ScanControl | undefined,
  maxPages = 100000,
): CollectResult<K, V> {
  const items: DiffItem<K, V>[] = [];
  const pages: DiffPage<K, V>[] = [];
  let cursor = start;
  let retryWithoutControl = false;
  for (let i = 0; i < maxPages; i++) {
    const control = retryWithoutControl ? undefined : controlFor?.(i);
    retryWithoutControl = false;
    const page = differ.nextPage(cursor, pageSize, control);
    pages.push(page);
    items.push(...page.items);
    if (page.done) return { items, pages };
    if (control !== undefined && page.items.length === 0 && page.cursor.lastKey === cursor.lastKey) {
      retryWithoutControl = true;
    }
    cursor = page.cursor;
  }
  throw new Error(`diff did not terminate within ${maxPages} pages`);
}
