/** 半开区间 [start, end)。 */
export interface Interval {
  start: number;
  end: number;
}

export function interval(start: number, end: number): Interval {
  if (!(start < end)) throw new RangeError(`非法区间 [${start}, ${end})：要求 start < end`);
  return { start, end };
}

export function overlaps(a: Interval, b: Interval): boolean {
  return a.start < b.end && b.start < a.end;
}

export function intersect(a: Interval, b: Interval): Interval | null {
  const start = Math.max(a.start, b.start);
  const end = Math.min(a.end, b.end);
  return start < end ? { start, end } : null;
}

/** 从 target 中减去 cover 的覆盖，返回未被覆盖的子区间列表（有序、不相交）。 */
export function subtract(target: Interval, covers: Interval[]): Interval[] {
  const sorted = covers
    .map((c) => intersect(target, c))
    .filter((c): c is Interval => c !== null)
    .sort((a, b) => a.start - b.start);

  const uncovered: Interval[] = [];
  let cursor = target.start;
  for (const c of sorted) {
    if (c.start > cursor) uncovered.push({ start: cursor, end: Math.min(c.start, target.end) });
    cursor = Math.max(cursor, c.end);
    if (cursor >= target.end) break;
  }
  if (cursor < target.end) uncovered.push({ start: cursor, end: target.end });
  return uncovered;
}

export function formatInterval(i: Interval): string {
  return `[${i.start}, ${i.end})`;
}
