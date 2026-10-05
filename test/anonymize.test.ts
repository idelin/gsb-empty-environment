import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { findOptimalScheme } from '../src/anonymize.ts';
import type { Dataset } from '../src/types.ts';

const twoColumnDataset: Dataset = {
  columns: ['a', 'b'],
  hierarchies: {
    a: { levels: [{ x1: 'X', x2: 'X' }] },
    b: { levels: [{ y1: 'Y', y2: 'Y' }] },
  },
  rows: [
    { a: 'x1', b: 'y1' },
    { a: 'x1', b: 'y1' },
    { a: 'x1', b: 'y1' },
    { a: 'x2', b: 'y1' },
    { a: 'x2', b: 'y1' },
    { a: 'x2', b: 'y1' },
    { a: 'x1', b: 'y2' },
  ],
};

test('选择总损失最小的统一列级方案', () => {
  const result = findOptimalScheme(twoColumnDataset, 3);
  assert.equal(result.feasible, true);
  if (!result.feasible) return;
  // (0,0) 与 (1,0) 均存在 1 人小组；(0,1) 可行且比 (1,1) 损失更低
  assert.deepEqual(result.levels, { a: 0, b: 1 });
  assert.equal(result.groupCount, 2);
  assert.equal(result.minGroupSize, 3);
  assert.deepEqual(
    result.groups.map((group) => group.count).sort((m, n) => m - n),
    [3, 4],
  );
  // 总损失 = 7 行 × (0/1 + 1/1) = 7
  assert.equal(result.totalLoss, 7);
  assert.equal(result.averageLoss, 7 / 14);
});

test('原始层级已满足 k 时保持零损失', () => {
  const dataset: Dataset = {
    columns: ['a'],
    hierarchies: { a: { levels: [{ x1: 'X', x2: 'X' }] } },
    rows: [
      { a: 'x1' },
      { a: 'x1' },
      { a: 'x1' },
      { a: 'x2' },
      { a: 'x2' },
      { a: 'x2' },
    ],
  };
  const result = findOptimalScheme(dataset, 3);
  assert.equal(result.feasible, true);
  if (!result.feasible) return;
  assert.deepEqual(result.levels, { a: 0 });
  assert.equal(result.totalLoss, 0);
  assert.equal(result.groupCount, 2);
});

test('总人数不足 k 时明确无解', () => {
  const dataset: Dataset = {
    columns: ['a'],
    hierarchies: { a: { levels: [{ x1: 'X' }] } },
    rows: [{ a: 'x1' }, { a: 'x1' }],
  };
  const result = findOptimalScheme(dataset, 3);
  assert.equal(result.feasible, false);
  if (result.feasible) return;
  assert.equal(result.reason, 'INSUFFICIENT_RECORDS');
  assert.match(result.message, /总人数 2 小于 k=3/);
});

test('最高层级仍有人数不足的组合时明确无解并列出违规分组', () => {
  const dataset: Dataset = {
    columns: ['a'],
    hierarchies: { a: { levels: [{ x1: 'X', x2: 'X' }] } },
    rows: [
      { a: 'x1' },
      { a: 'x1' },
      { a: 'x1' },
      { a: 'x2' },
    ],
  };
  // x2 只有 1 行，但层级可合并：x1/x2 -> X，最高层 4 人一组，可行
  const merged = findOptimalScheme(dataset, 3);
  assert.equal(merged.feasible, true);

  const stuck: Dataset = {
    columns: ['a', 'b'],
    hierarchies: {
      a: { levels: [{ x1: 'X' }] },
      b: { levels: [] },
    },
    rows: [
      { a: 'x1', b: 'y1' },
      { a: 'x1', b: 'y1' },
      { a: 'x1', b: 'y1' },
      { a: 'x1', b: 'y2' },
    ],
  };
  const result = findOptimalScheme(stuck, 3);
  assert.equal(result.feasible, false);
  if (result.feasible) return;
  assert.equal(result.reason, 'NO_FEASIBLE_SCHEME');
  assert.equal(result.violatingGroups?.length, 1);
  assert.equal(result.violatingGroups?.[0]?.count, 1);
  assert.equal(result.violatingGroups?.[0]?.values.b, 'y2');
});

test('空值独立处理：不随层级提升，只与其他空值同组', () => {
  const dataset: Dataset = {
    columns: ['a', 'b'],
    hierarchies: {
      a: { levels: [{ x1: 'X', x2: 'X' }] },
      b: { levels: [{ y1: 'Y' }] },
    },
    rows: [
      { a: 'x1', b: null },
      { a: 'x2', b: null },
      { a: 'x1', b: null },
      { a: 'x1', b: 'y1' },
      { a: 'x2', b: 'y1' },
      { a: 'x2', b: 'y1' },
    ],
  };
  const result = findOptimalScheme(dataset, 3);
  assert.equal(result.feasible, true);
  if (!result.feasible) return;
  // 空值组与非空值组各自恰好 3 人；空值在任何层级都保持 null
  assert.equal(result.groupCount, 2);
  const nullGroup = result.groups.find((group) => group.values.b === null);
  assert.equal(nullGroup?.count, 3);
  const valueGroup = result.groups.find((group) => group.values.b !== null);
  assert.equal(valueGroup?.count, 3);
});

test('空值人数不足且无法与非空值合并时无解', () => {
  const dataset: Dataset = {
    columns: ['a'],
    hierarchies: { a: { levels: [{ x1: 'X' }] } },
    rows: [{ a: null }, { a: null }, { a: 'x1' }, { a: 'x1' }, { a: 'x1' }],
  };
  const result = findOptimalScheme(dataset, 3);
  assert.equal(result.feasible, false);
  if (result.feasible) return;
  assert.equal(result.reason, 'NO_FEASIBLE_SCHEME');
  assert.equal(result.violatingGroups?.[0]?.values.a, null);
  assert.equal(result.violatingGroups?.[0]?.count, 2);
});

test('层级映射缺失时报错指出列与取值', () => {
  const dataset: Dataset = {
    columns: ['a'],
    hierarchies: { a: { levels: [{ x1: 'X' }] } },
    rows: [{ a: 'oops' }],
  };
  assert.throws(() => findOptimalScheme(dataset, 2), /列 "a" 的值 "oops"/);
});
