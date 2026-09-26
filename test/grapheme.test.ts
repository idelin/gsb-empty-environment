import { test } from 'node:test';
import assert from 'node:assert/strict';
import { segmentGraphemes, flatten } from '../src/index.ts';

test('emoji 组合簇(ZWJ 序列)不被切开', () => {
  const clusters = segmentGraphemes('a👨‍👩‍👧b');
  assert.deepEqual(clusters, ['a', '👨‍👩‍👧', 'b']);
});

test('国旗、肤色修饰符、组合记号各自成簇', () => {
  assert.deepEqual(segmentGraphemes('🇨🇳x'), ['🇨🇳', 'x']);
  assert.deepEqual(segmentGraphemes('👍🏽y'), ['👍🏽', 'y']);
  assert.deepEqual(segmentGraphemes('éz'), ['é', 'z']);
});

test('实体展平为单个原子,内部不可拆', () => {
  const { atoms } = flatten([
    { kind: 'text', text: 'ab' },
    { kind: 'entity', entityId: 'E1', text: '@channel' },
    { kind: 'text', text: 'cd' },
  ]);
  const entityAtoms = atoms.filter((a) => a.kind === 'entity');
  assert.equal(entityAtoms.length, 1);
  assert.equal(entityAtoms[0].text, '@channel');
  assert.equal(entityAtoms[0].start, 2);
  assert.equal(entityAtoms[0].end, 10);
});

test('展平产出完整可访问名称与链接范围', () => {
  const { atoms, accessibleName } = flatten([
    { kind: 'text', text: 'see ' },
    { kind: 'link', linkId: 'L', children: [{ kind: 'text', text: 'docs' }] },
  ]);
  assert.equal(accessibleName, 'see docs');
  const linkAtoms = atoms.filter((a) => a.linkId === 'L');
  assert.deepEqual(linkAtoms[0].linkRange, { start: 4, end: 8 });
});
