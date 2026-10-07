import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import {
  groupsFromRows,
  paginate,
  PaginationOverflowError,
} from '../src/index.ts';
import type { RowGroup, TableRow } from '../src/index.ts';

function rowsOf(heights: number[]): TableRow[] {
  return heights.map((height, i) => ({ id: `r${i}`, height }));
}

function singletons(heights: number[]): RowGroup[] {
  return groupsFromRows(rowsOf(heights));
}

function pageGroupIds(page: { groups: RowGroup[] }): string[] {
  return page.groups.map((g) => g.id);
}

function captureOverflow(fn: () => unknown): PaginationOverflowError {
  try {
    fn();
  } catch (error) {
    assert.ok(error instanceof PaginationOverflowError, '应抛出 PaginationOverflowError');
    return error;
  }
  assert.fail('应抛出 PaginationOverflowError，但实际未抛错');
}

describe('基本分页', () => {
  test('内容一页放下时只有一页，同时占用表头与尾注高度', () => {
    const pages = paginate(singletons([10, 20]), {
      pageHeight: 100,
      headerHeight: 10,
      footnoteHeight: 15,
    });
    assert.equal(pages.length, 1);
    assert.equal(pages[0].isLast, true);
    assert.equal(pages[0].contentHeight, 30);
    assert.equal(pages[0].usedHeight, 10 + 30 + 15);
  });

  test('表头在每页重复占高', () => {
    const pages = paginate(singletons([30, 30, 30]), {
      pageHeight: 50,
      headerHeight: 10,
    });
    assert.equal(pages.length, 3);
    for (const page of pages) {
      assert.equal(page.headerHeight, 10);
      assert.equal(page.usedHeight, 10 + 30);
      assert.ok(page.usedHeight <= 50);
    }
  });

  test('行组高度恰好等于内容区容量时整组落页', () => {
    const pages = paginate(singletons([40, 10]), {
      pageHeight: 50,
      headerHeight: 10,
    });
    assert.equal(pages.length, 2);
    assert.deepEqual(pageGroupIds(pages[0]), ['row:r0']);
    assert.deepEqual(pageGroupIds(pages[1]), ['row:r1']);
  });

  test('空文档产生一页（仅表头与尾注）', () => {
    const pages = paginate([], {
      pageHeight: 100,
      headerHeight: 10,
      footnoteHeight: 20,
    });
    assert.equal(pages.length, 1);
    assert.equal(pages[0].groups.length, 0);
    assert.equal(pages[0].usedHeight, 30);
  });
});

describe('合并单元格行组不可拆', () => {
  test('跨行合并的组在页边界处整体移到下一页', () => {
    const rows = rowsOf([30, 20, 20, 30]);
    const groups = groupsFromRows(rows, [{ start: 1, end: 2 }]);
    const pages = paginate(groups, { pageHeight: 60, headerHeight: 10 });
    // 内容区容量 50：r0(30) 落第 1 页后，合并组(20+20=40) 放不下，整体换页。
    assert.equal(pages.length, 3);
    assert.deepEqual(pageGroupIds(pages[0]), ['row:r0']);
    assert.deepEqual(pageGroupIds(pages[1]), ['merge:r1..r2']);
    assert.deepEqual(pageGroupIds(pages[2]), ['row:r3']);
    // 合并组的两行在同一页且连续。
    assert.deepEqual(pages[1].groups[0].rows.map((r) => r.id), ['r1', 'r2']);
  });

  test('相邻合并范围各自成组', () => {
    const rows = rowsOf([10, 10, 10, 10]);
    const groups = groupsFromRows(rows, [
      { start: 0, end: 1 },
      { start: 2, end: 3 },
    ]);
    assert.deepEqual(groups.map((g) => g.id), ['merge:r0..r1', 'merge:r2..r3']);
  });

  test('非法合并范围报错', () => {
    const rows = rowsOf([10, 10, 10]);
    assert.throws(() => groupsFromRows(rows, [{ start: 1, end: 1 }]), RangeError);
    assert.throws(() => groupsFromRows(rows, [{ start: 1, end: 3 }]), RangeError);
    assert.throws(
      () => groupsFromRows(rows, [{ start: 0, end: 1 }, { start: 1, end: 2 }]),
      /重叠/,
    );
  });
});

describe('超页报错，不无限新建页', () => {
  test('行组加表头超页时报出具体组', () => {
    const groups = singletons([10, 95, 10]);
    const error = captureOverflow(() =>
      paginate(groups, { pageHeight: 100, headerHeight: 10 }),
    );
    assert.equal(error.kind, 'group');
    assert.equal(error.groupId, 'row:r1');
    assert.equal(error.groupIndex, 1);
    assert.equal(error.requiredHeight, 95);
    assert.equal(error.availableHeight, 90);
    assert.match(error.message, /row:r1/);
    assert.match(error.message, /第 1 组/);
  });

  test('合并组整体超页时报该合并组', () => {
    const groups = groupsFromRows(rowsOf([50, 50]), [{ start: 0, end: 1 }]);
    const error = captureOverflow(() =>
      paginate(groups, { pageHeight: 100, headerHeight: 10 }),
    );
    assert.equal(error.kind, 'group');
    assert.equal(error.groupId, 'merge:r0..r1');
    assert.equal(error.requiredHeight, 100);
  });

  test('表头占满整页时报 header 错误', () => {
    const error = captureOverflow(() =>
      paginate(singletons([10]), { pageHeight: 30, headerHeight: 30 }),
    );
    assert.equal(error.kind, 'header');
  });

  test('尾注超过末页内容区时报 footnote 错误', () => {
    const error = captureOverflow(() =>
      paginate(singletons([10]), {
        pageHeight: 100,
        headerHeight: 10,
        footnoteHeight: 95,
      }),
    );
    assert.equal(error.kind, 'footnote');
    assert.equal(error.availableHeight, 90);
  });

  test('非法参数直接拒绝', () => {
    assert.throws(() => paginate(singletons([10]), { pageHeight: 0, headerHeight: 0 }), RangeError);
    assert.throws(
      () => paginate(singletons([10]), { pageHeight: 10, headerHeight: -1 }),
      RangeError,
    );
    assert.throws(
      () => paginate([{ id: 'g0', rows: [] }], { pageHeight: 10, headerHeight: 1 }),
      RangeError,
    );
    assert.throws(
      () => paginate(singletons([Number.NaN]), { pageHeight: 10, headerHeight: 1 }),
      TypeError,
    );
  });
});

describe('尾注仅末页占高与末页回流', () => {
  test('尾注只出现在最后一页', () => {
    const pages = paginate(singletons([30, 30, 30]), {
      pageHeight: 50,
      headerHeight: 10,
      footnoteHeight: 8,
    });
    assert.equal(pages.length, 3);
    assert.deepEqual(pages.map((p) => p.footnoteHeight), [0, 0, 8]);
    assert.equal(pages[2].usedHeight, 10 + 30 + 8);
  });

  test('尾注占高触发回流：内容被挤到末页而非产生空白页', () => {
    // 容量 90，末页容量 70。两组共 85：无尾注时一页放下，
    // 有尾注时末页放不下，应回流为 [40] / [45] 两页。
    const options = { pageHeight: 100, headerHeight: 10, footnoteHeight: 20 };
    const without = paginate(singletons([40, 45]), { ...options, footnoteHeight: 0 });
    assert.equal(without.length, 1);
    const pages = paginate(singletons([40, 45]), options);
    assert.equal(pages.length, 2);
    assert.deepEqual(pageGroupIds(pages[0]), ['row:r0']);
    assert.deepEqual(pageGroupIds(pages[1]), ['row:r1']);
    assert.equal(pages[1].usedHeight, 10 + 45 + 20);
  });

  test('末组无法放入末页容量时允许空白末页承载尾注', () => {
    // 容量 90，末页容量 70：80 的组永远不能落在末页。
    const pages = paginate(singletons([40, 80]), {
      pageHeight: 100,
      headerHeight: 10,
      footnoteHeight: 20,
    });
    assert.equal(pages.length, 3);
    assert.deepEqual(pageGroupIds(pages[0]), ['row:r0']);
    assert.deepEqual(pageGroupIds(pages[1]), ['row:r1']);
    assert.deepEqual(pageGroupIds(pages[2]), []);
    assert.equal(pages[2].usedHeight, 10 + 0 + 20);
  });

  test('尾注高度恰好等于内容区容量时末页只能放空', () => {
    const pages = paginate(singletons([10, 10]), {
      pageHeight: 100,
      headerHeight: 10,
      footnoteHeight: 90,
    });
    assert.equal(pages.length, 2);
    assert.deepEqual(pageGroupIds(pages[0]), ['row:r0', 'row:r1']);
    assert.deepEqual(pageGroupIds(pages[1]), []);
  });
});
