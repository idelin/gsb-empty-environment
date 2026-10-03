/** 半开区间 [start, end)。 */
export interface Interval {
  start: number;
  end: number;
}

export function isValid(interval: Interval): boolean {
  return interval.start < interval.end;
}

/** outer 是否完整覆盖 inner。 */
export function covers(outer: Interval, inner: Interval): boolean {
  return outer.start <= inner.start && inner.end <= outer.end;
}

export function overlaps(a: Interval, b: Interval): boolean {
  return a.start < b.end && b.start < a.end;
}

export function intersect(a: Interval, b: Interval): Interval | null {
  const start = Math.max(a.start, b.start);
  const end = Math.min(a.end, b.end);
  return start < end ? { start, end } : null;
}

/** 合并重叠或相邻的区间，返回按 start 排序的不相交区间列表。 */
export function union(intervals: Interval[]): Interval[] {
  const sorted = intervals
    .filter(isValid)
    .slice()
    .sort((a, b) => a.start - b.start || a.end - b.end);
  const merged: Interval[] = [];
  for (const interval of sorted) {
    const last = merged[merged.length - 1];
    if (last && interval.start <= last.end) {
      last.end = Math.max(last.end, interval.end);
    } else {
      merged.push({ ...interval });
    }
  }
  return merged;
}

/** 计算 target 减去 blocks 后剩余的缺口区间（即 target 中未被覆盖的部分）。 */
export function subtract(target: Interval, blocks: Interval[]): Interval[] {
  const gaps: Interval[] = [];
  let cursor = target.start;
  for (const block of union(blocks)) {
    if (block.end <= cursor) continue;
    if (block.start >= target.end) break;
    if (block.start > cursor) {
      gaps.push({ start: cursor, end: Math.min(block.start, target.end) });
    }
    cursor = Math.max(cursor, block.end);
    if (cursor >= target.end) return gaps;
  }
  if (cursor < target.end) {
    gaps.push({ start: cursor, end: target.end });
  }
  return gaps;
}
