import type { RowGroup, TableRow } from './types.ts';

/** 合并单元格的跨行范围，下标闭区间 [start, end]。 */
export interface MergeRange {
  start: number;
  end: number;
}

/** 行组高度 = 组内各行高度之和。 */
export function groupHeight(group: RowGroup): number {
  let total = 0;
  for (const row of group.rows) total += row.height;
  return total;
}

/**
 * 由行列表与合并单元格范围构建不可拆行组：
 * 被合并范围覆盖的行聚为一个行组，其余行各自成为单行组。
 * 范围必须落在行内、跨至少两行、互不重叠（允许相邻）。
 */
export function groupsFromRows(rows: TableRow[], merges: MergeRange[] = []): RowGroup[] {
  const sorted = [...merges].sort((a, b) => a.start - b.start);
  let prevEnd = -1;
  for (const range of sorted) {
    const { start, end } = range;
    if (!Number.isInteger(start) || !Number.isInteger(end)) {
      throw new RangeError(`合并范围下标必须为整数，收到 [${start}, ${end}]`);
    }
    if (start < 0 || end >= rows.length) {
      throw new RangeError(`合并范围 [${start}, ${end}] 超出行列表（共 ${rows.length} 行）`);
    }
    if (end <= start) {
      throw new RangeError(`合并范围 [${start}, ${end}] 至少要跨两行才有意义`);
    }
    if (start <= prevEnd) {
      throw new RangeError(`合并范围 [${start}, ${end}] 与之前的范围重叠`);
    }
    prevEnd = end;
  }

  const groups: RowGroup[] = [];
  let cursor = 0;
  for (const { start, end } of sorted) {
    while (cursor < start) {
      groups.push(singleton(rows[cursor]));
      cursor += 1;
    }
    const slice = rows.slice(start, end + 1);
    groups.push({
      id: `merge:${slice[0].id}..${slice[slice.length - 1].id}`,
      rows: slice,
    });
    cursor = end + 1;
  }
  while (cursor < rows.length) {
    groups.push(singleton(rows[cursor]));
    cursor += 1;
  }
  return groups;
}

function singleton(row: TableRow): RowGroup {
  return { id: `row:${row.id}`, rows: [row] };
}
