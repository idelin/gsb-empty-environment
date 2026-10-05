/**
 * Reference diff: materialize both maps and merge-compare in one pass.
 * O(n + m) time and memory, no skipping, no pagination — used as the
 * ground truth the paginated engine is checked against.
 */

import type { PMap } from './avl.ts';
import type { DiffItem } from './diff.ts';

export function referenceDiff<K, V>(
  left: PMap<K, V>,
  right: PMap<K, V>,
  valueEquals: (a: V, b: V) => boolean = Object.is,
): DiffItem<K, V>[] {
  const items: DiffItem<K, V>[] = [];
  const leftIter = left.entries();
  const rightIter = right.entries();
  let l = leftIter.next();
  let r = rightIter.next();
  while (!l.done || !r.done) {
    if (r.done) {
      items.push({ type: 'removed', key: l.value![0], value: l.value![1] });
      l = leftIter.next();
      continue;
    }
    if (l.done) {
      items.push({ type: 'added', key: r.value![0], value: r.value![1] });
      r = rightIter.next();
      continue;
    }
    const [lk, lv] = l.value!;
    const [rk, rv] = r.value!;
    const cmp = left.compare(lk, rk);
    if (cmp < 0) {
      items.push({ type: 'removed', key: lk, value: lv });
      l = leftIter.next();
    } else if (cmp > 0) {
      items.push({ type: 'added', key: rk, value: rv });
      r = rightIter.next();
    } else {
      if (!valueEquals(lv, rv)) {
        items.push({ type: 'changed', key: lk, oldValue: lv, newValue: rv });
      }
      l = leftIter.next();
      r = rightIter.next();
    }
  }
  return items;
}
