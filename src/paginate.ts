import { PaginationOverflowError } from './errors.ts';
import { groupHeight } from './groups.ts';
import type { Page, PaginateOptions, RowGroup, TableRow } from './types.ts';

/** 浮点高度求和的容差。 */
const EPSILON = 1e-9;

/**
 * 按行高把行组序列切分为页。
 *
 * 规则：
 * - 每页预留表头高度，内容区容量 = pageHeight - headerHeight；
 * - 行组（合并单元格行组）不可拆分，要么整组落页要么整组换页；
 * - 尾注仅在最后一页占高，因此末页容量 = 内容区容量 - footnoteHeight，
 *   放不下时会触发末页回流（末组被挤到上一页布局重新计算）；
 * - 单个行组加表头超过整页时抛出 PaginationOverflowError 并指明具体组，
 *   分页搜索上界为 n+1 页，保证有界终止，绝不无限新建页。
 */
export function paginate(groups: RowGroup[], options: PaginateOptions): Page[] {
  const { pageHeight, headerHeight } = options;
  const footnoteHeight = options.footnoteHeight ?? 0;
  assertFinite('pageHeight', pageHeight);
  assertFinite('headerHeight', headerHeight);
  assertFinite('footnoteHeight', footnoteHeight);
  if (pageHeight <= 0) throw new RangeError(`pageHeight 必须为正数，收到 ${pageHeight}`);
  if (headerHeight < 0) throw new RangeError(`headerHeight 不能为负，收到 ${headerHeight}`);
  if (footnoteHeight < 0) throw new RangeError(`footnoteHeight 不能为负，收到 ${footnoteHeight}`);

  const contentCapacity = pageHeight - headerHeight;
  if (contentCapacity <= 0) {
    throw new PaginationOverflowError({
      kind: 'header',
      requiredHeight: headerHeight,
      availableHeight: pageHeight,
    });
  }
  if (footnoteHeight > contentCapacity + EPSILON) {
    throw new PaginationOverflowError({
      kind: 'footnote',
      requiredHeight: footnoteHeight,
      availableHeight: contentCapacity,
    });
  }

  const heights = groups.map((group, index) => {
    validateGroup(group, index);
    const height = groupHeight(group);
    if (height > contentCapacity + EPSILON) {
      throw new PaginationOverflowError({
        kind: 'group',
        groupId: group.id,
        groupIndex: index,
        requiredHeight: height,
        availableHeight: contentCapacity,
      });
    }
    return height;
  });

  const n = heights.length;
  const lastCapacity = contentCapacity - footnoteHeight;

  // suffixSum[i] = 第 i 组到末尾的高度之和（suffixSum[n] = 0）。
  const suffixSum = new Array<number>(n + 1).fill(0);
  for (let i = n - 1; i >= 0; i -= 1) suffixSum[i] = suffixSum[i + 1] + heights[i];

  // packed[p] = 按“满容量页贪心装到最满”时，p 个普通页最多能装下的组数。
  // 普通页容量相同，贪心前缀装载是最优的（交换论证），因此末页剩余高度最小。
  const packed: number[] = [0];
  for (let p = 1; p <= n; p += 1) {
    let used = 0;
    let i = packed[p - 1];
    while (i < n && used + heights[i] <= contentCapacity + EPSILON) {
      used += heights[i];
      i += 1;
    }
    packed.push(i);
  }

  // 枚举总页数 k（普通页 k-1 个 + 末页 1 个），取第一个可行的 k。
  // packed[n] === n 且 suffixSum[n] === 0 <= lastCapacity，故 k = n + 1 必然成功：
  // 循环有界，不会无限新建页。
  for (let pageCount = 1; pageCount <= n + 1; pageCount += 1) {
    let split = packed[pageCount - 1];
    if (suffixSum[split] > lastCapacity + EPSILON) continue;
    if (split === n && n > 0 && suffixSum[n - 1] <= lastCapacity + EPSILON) {
      // 贪心把内容全装进普通页、末页只剩表头尾注时，
      // 优先把最后一组回流到末页，避免产生空白内容页。
      split = n - 1;
    }
    return buildPages(groups, heights, split, contentCapacity, headerHeight, footnoteHeight);
  }
  throw new Error('paginate: 内部错误，页数搜索越界');
}

function buildPages(
  groups: RowGroup[],
  heights: number[],
  split: number,
  contentCapacity: number,
  headerHeight: number,
  footnoteHeight: number,
): Page[] {
  const pages: Page[] = [];
  let current: RowGroup[] = [];
  let used = 0;
  for (let i = 0; i < split; i += 1) {
    if (current.length > 0 && used + heights[i] > contentCapacity + EPSILON) {
      pages.push(makePage(pages.length, current, used, headerHeight, 0, false));
      current = [];
      used = 0;
    }
    current.push(groups[i]);
    used += heights[i];
  }
  if (current.length > 0) {
    pages.push(makePage(pages.length, current, used, headerHeight, 0, false));
  }

  const lastGroups = groups.slice(split);
  let lastContent = 0;
  for (let i = split; i < heights.length; i += 1) lastContent += heights[i];
  pages.push(makePage(pages.length, lastGroups, lastContent, headerHeight, footnoteHeight, true));
  return pages;
}

function makePage(
  index: number,
  groups: RowGroup[],
  contentHeight: number,
  headerHeight: number,
  footnoteHeight: number,
  isLast: boolean,
): Page {
  return {
    index,
    groups,
    contentHeight,
    headerHeight,
    footnoteHeight,
    usedHeight: headerHeight + contentHeight + footnoteHeight,
    isLast,
  };
}

function validateGroup(group: RowGroup, index: number): void {
  if (!Array.isArray(group.rows) || group.rows.length === 0) {
    throw new RangeError(`第 ${index} 个行组（"${group.id}"）不能为空，至少包含一行`);
  }
  for (const row of group.rows) {
    validateRow(row, group.id);
  }
}

function validateRow(row: TableRow, groupId: string): void {
  assertFinite(`行 "${row.id}" 的 height`, row.height);
  if (row.height < 0) {
    throw new RangeError(`行 "${row.id}"（组 "${groupId}"）高度不能为负，收到 ${row.height}`);
  }
}

function assertFinite(name: string, value: number): void {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new TypeError(`${name} 必须是有限数值，收到 ${String(value)}`);
  }
}
