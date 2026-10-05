import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { findOptimalScheme } from '../src/anonymize.ts';
import type { Dataset } from '../src/types.ts';

test('示例数据集端到端求解', () => {
  const sample = JSON.parse(
    readFileSync(new URL('../examples/sample.json', import.meta.url), 'utf8'),
  ) as Dataset & { k: number };
  const result = findOptimalScheme(sample, sample.k ?? 3);
  assert.equal(result.feasible, true);
  if (!result.feasible) return;
  assert.ok(result.minGroupSize >= 3);
  const total = result.groups.reduce((sum, group) => sum + group.count, 0);
  assert.equal(total, sample.rows.length);
  // 3 行 gender 为空的记录应独立成组，且不与泛化后的 "*" 合并
  const nullGroups = result.groups.filter((group) => group.values.gender === null);
  assert.equal(nullGroups.length, 1);
  assert.equal(nullGroups[0]?.count, 3);
});
