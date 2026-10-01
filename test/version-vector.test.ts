import { test } from 'node:test';
import assert from 'node:assert/strict';
import { VersionVector } from '../src/version-vector.ts';

test('空向量与构造', () => {
  const vv = VersionVector.empty();
  assert.ok(vv.isEmpty());
  assert.equal(vv.get('R1'), 0);
  assert.deepEqual(VersionVector.from({ R1: 2, R2: 0 }).toJSON(), { R1: 2 });
});

test('tick 递增本副本计数且不可变', () => {
  const v1 = VersionVector.empty().tick('R1');
  const v2 = v1.tick('R1');
  assert.equal(v1.get('R1'), 1);
  assert.equal(v2.get('R1'), 2);
  assert.equal(v2.toString(), '{R1:2}');
});

test('merge 取逐分量最大值', () => {
  const a = VersionVector.from({ R1: 2, R2: 1 });
  const b = VersionVector.from({ R1: 1, R3: 5 });
  assert.deepEqual(a.merge(b).toJSON(), { R1: 2, R2: 1, R3: 5 });
});

test('compare 四种偏序关系', () => {
  const a = VersionVector.from({ R1: 1 });
  const b = VersionVector.from({ R1: 2 });
  const c = VersionVector.from({ R2: 1 });
  assert.equal(a.compare(a), 'equal');
  assert.equal(a.compare(b), 'before');
  assert.equal(b.compare(a), 'after');
  assert.equal(a.compare(c), 'concurrent');
  assert.ok(b.dominates(a));
  assert.ok(!a.dominates(c));
});

test('非法计数抛错', () => {
  assert.throws(() => VersionVector.from({ R1: -1 }));
  assert.throws(() => VersionVector.from({ R1: 1.5 }));
});
