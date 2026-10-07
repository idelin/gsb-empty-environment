import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { paginate, PaginationOverflowError } from '../src/index.ts';
import type { Page, PaginateOptions, RowGroup } from '../src/index.ts';

const EPS = 1e-9;

interface Config {
  pageHeight: number;
  headerHeight: number;
  footnoteHeight: number;
}

const CONFIGS: Config[] = [
  { pageHeight: 20, headerHeight: 3, footnoteHeight: 0 },
  { pageHeight: 20, headerHeight: 3, footnoteHeight: 4 },
  { pageHeight: 20, headerHeight: 3, footnoteHeight: 12 },
  { pageHeight: 14, headerHeight: 2, footnoteHeight: 6 },
  { pageHeight: 11, headerHeight: 2, footnoteHeight: 9 }, // 末页内容容量为 0
];

/** 两套确定性行高分布，覆盖不同奇偶/周期。 */
function heightSets(n: number): number[][] {
  return [
    Array.from({ length: n }, (_, r) => 2 + ((r * 3 + n) % 5)),
    Array.from({ length: n }, (_, r) => 1 + ((r * r + r + n) % 4)),
  ];
}

/** 枚举 n 行的全部 2^(n-1) 种“划分成不可拆行组”的方式（每个行间隙可断可合）。 */
function compositions(n: number): number[][] {
  const result: number[][] = [];
  const masks = 1 << Math.max(0, n - 1);
  for (let mask = 0; mask < masks; mask += 1) {
    const sizes: number[] = [];
    let run = 1;
    for (let gap = 0; gap < n - 1; gap += 1) {
      if (mask & (1 << gap)) {
        sizes.push(run);
        run = 1;
      } else {
        run += 1;
      }
    }
    sizes.push(run);
    result.push(sizes);
  }
  return result;
}

function buildGroups(heights: number[], sizes: number[]): RowGroup[] {
  const groups: RowGroup[] = [];
  let offset = 0;
  for (const size of sizes) {
    const rows = heights
      .slice(offset, offset + size)
      .map((height, j) => ({ id: `r${offset + j}`, height }));
    groups.push({
      id: size > 1 ? `merge:r${offset}..r${offset + size - 1}` : `row:r${offset}`,
      rows,
    });
    offset += size;
  }
  return groups;
}

function heightsOf(groups: RowGroup[]): number[] {
  return groups.map((g) => g.rows.reduce((sum, r) => sum + r.height, 0));
}

/**
 * 独立 oracle：暴力枚举所有“切分为 k 页”的序列划分，
 * 普通页非空且不超过 cap，末页（可为空）不超过 lastCap，返回最小可行页数。
 */
function oracleMinPages(heights: number[], cap: number, lastCap: number): number {
  const n = heights.length;
  const suffix = new Array<number>(n + 1).fill(0);
  for (let i = n - 1; i >= 0; i -= 1) suffix[i] = suffix[i + 1] + heights[i];

  function feasible(k: number): boolean {
    function rec(page: number, i: number): boolean {
      if (page === k - 1) return suffix[i] <= lastCap + EPS;
      let used = 0;
      for (let j = i; j < n; j += 1) {
        used += heights[j];
        if (used > cap + EPS) break;
        if (rec(page + 1, j + 1)) return true;
      }
      return false;
    }
    return rec(0, 0);
  }

  for (let k = 1; k <= n + 1; k += 1) {
    if (feasible(k)) return k;
  }
  throw new Error('oracle 无解（不应发生）');
}

function assertLayout(
  pages: Page[],
  groups: RowGroup[],
  groupHeights: number[],
  cfg: Config,
): void {
  const cap = cfg.pageHeight - cfg.headerHeight;
  const lastCap = cap - cfg.footnoteHeight;
  assert.ok(pages.length >= 1, '至少要有一页');

  // 顺序保持且完整覆盖：各页行组拼接回原序列。
  assert.deepEqual(
    pages.flatMap((p) => p.groups.map((g) => g.id)),
    groups.map((g) => g.id),
  );
  // 行组原子性：每个不可拆组只出现在一页。
  const ids = pages.flatMap((p) => p.groups.map((g) => g.id));
  assert.equal(new Set(ids).size, ids.length, '行组被拆到多页');

  pages.forEach((page, idx) => {
    assert.equal(page.index, idx);
    assert.equal(page.isLast, idx === pages.length - 1);
    assert.equal(page.headerHeight, cfg.headerHeight, '每页都必须重复表头');
    assert.ok(
      Math.abs(page.usedHeight - (page.headerHeight + page.contentHeight + page.footnoteHeight)) < EPS,
      'usedHeight 账目不符',
    );
    assert.ok(page.usedHeight <= cfg.pageHeight + EPS, `第 ${idx} 页超高`);
    if (page.isLast) {
      assert.equal(page.footnoteHeight, cfg.footnoteHeight, '尾注必须落在末页');
      assert.ok(page.contentHeight <= lastCap + EPS, '末页未给尾注留高');
    } else {
      assert.equal(page.footnoteHeight, 0, '尾注只能出现在末页');
      assert.ok(page.groups.length > 0, '非末页不能为空页');
      assert.ok(page.contentHeight <= cap + EPS, '普通页超容量');
    }
  });

  // 不无限新建页：页数有 n+1 的硬上界。
  assert.ok(pages.length <= groups.length + 1, '页数超过 n+1 上界');
  // 与独立 oracle 核对：页数必须是最少可行页数。
  assert.equal(
    pages.length,
    oracleMinPages(groupHeights, cap, lastCap),
    `页数不是最优（组高 ${groupHeights}，配置 ${JSON.stringify(cfg)}）`,
  );
}

describe('小规模划分枚举核对', () => {
  for (let n = 1; n <= 7; n += 1) {
    test(`n=${n} 行 × 全部 ${1 << (n - 1)} 种行组划分 × ${CONFIGS.length} 种页面配置`, () => {
      for (const heights of heightSets(n)) {
        for (const sizes of compositions(n)) {
          const groups = buildGroups(heights, sizes);
          const groupHeights = heightsOf(groups);
          for (const cfg of CONFIGS) {
            const cap = cfg.pageHeight - cfg.headerHeight;
            const firstBad = groupHeights.findIndex((h) => h > cap + EPS);
            if (firstBad >= 0) {
              // 行组加表头超页：必须报出具体组，且直接抛错而非无限新建页。
              let thrown: unknown;
              try {
                paginate(groups, cfg);
              } catch (error) {
                thrown = error;
              }
              assert.ok(thrown instanceof PaginationOverflowError, '应抛出 PaginationOverflowError');
              const err = thrown;
              assert.equal(err.kind, 'group');
              assert.equal(err.groupIndex, firstBad);
              assert.equal(err.groupId, groups[firstBad]?.id);
              continue;
            }
            const pages = paginate(groups, cfg);
            assertLayout(pages, groups, groupHeights, cfg);
          }
        }
      }
    });
  }
});

describe('末页回流交叉核对（有尾注 vs 无尾注）', () => {
  const distribution = (pages: Page[]): string =>
    pages.map((p) => p.groups.map((g) => g.id).join('+')).join(' | ');

  for (let n = 1; n <= 6; n += 1) {
    test(`n=${n} 行：尾注放得下则不扰动布局，放不下则必须回流`, () => {
      for (const heights of heightSets(n)) {
        for (const sizes of compositions(n)) {
          const groups = buildGroups(heights, sizes);
          const groupHeights = heightsOf(groups);
          for (const cfg of CONFIGS) {
            if (cfg.footnoteHeight === 0) continue;
            const cap = cfg.pageHeight - cfg.headerHeight;
            if (groupHeights.some((h) => h > cap + EPS)) continue;

            const baseOptions: PaginateOptions = {
              pageHeight: cfg.pageHeight,
              headerHeight: cfg.headerHeight,
            };
            const without = paginate(groups, { ...baseOptions, footnoteHeight: 0 });
            const withFoot = paginate(groups, cfg);
            const lastWithout = without[without.length - 1];
            const lastContent = lastWithout ? lastWithout.contentHeight : 0;

            assert.ok(withFoot.length >= without.length, '加尾注不应减少页数');
            if (lastContent <= cap - cfg.footnoteHeight + EPS) {
              // 末页本来就放得下尾注：布局必须原样保留。
              assert.equal(withFoot.length, without.length);
              assert.equal(distribution(withFoot), distribution(without));
            } else {
              // 放不下：必须回流——页数增加或页面分布改变。
              assert.ok(
                withFoot.length > without.length ||
                  distribution(withFoot) !== distribution(without),
                `尾注挤占末页却未回流（组高 ${groupHeights}，配置 ${JSON.stringify(cfg)}）`,
              );
            }
          }
        }
      }
    });
  }
});
