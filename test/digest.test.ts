import test from 'node:test';
import assert from 'node:assert/strict';
import { buildDigest, digestsEqual, normalizeTree } from '../src/digest.ts';
import { diffTrees } from '../src/diff.ts';
import { applyPatch } from '../src/patch.ts';
import type { HashFn } from '../src/hash.ts';

/** 强制碰撞：所有输入得到同一摘要。 */
const collidingHash: HashFn = () => '0';

test('强制摘要函数碰撞仍发现不同内容（碰撞复核）', () => {
  const a = { x: 1, list: [1, 2, 3], nested: { k: 'a' } };
  const b = { x: 2, list: [1, 2, 3], nested: { k: 'a' } };

  // 摘要完全相同（被迫碰撞），但内容比较能区分。
  const da = buildDigest(a, collidingHash);
  const db = buildDigest(b, collidingHash);
  assert.equal(da.digest, db.digest);
  assert.equal(digestsEqual(da, db), false, '碰撞复核应发现内容不同');
  assert.equal(digestsEqual(da, buildDigest(a, collidingHash)), true, '同内容应判等');

  // 差异定位到具体路径。
  const ops = diffTrees(a, b, { hash: collidingHash });
  assert.deepEqual(ops, [{ op: 'set', path: ['x'], value: 2, oldValue: 1 }]);

  // 相同内容在碰撞哈希下也不产生伪差异。
  assert.deepEqual(diffTrees(a, a, { hash: collidingHash }), []);

  // 碰撞哈希下补丁依然可用。
  const out = applyPatch(a, { ops }, { hash: collidingHash });
  assert.deepEqual(out, b);
});

test('规范化幂等且摘要与键序无关', () => {
  const messy = { z: [{ b: 2, a: 1 }, { d: { y: 1, x: 2 } }], a: { y: 1, x: 2 } };
  const once = normalizeTree(messy);
  const twice = normalizeTree(once);
  assert.deepEqual(twice, once, '规范化应幂等');
  assert.deepEqual(Object.keys(once), ['a', 'z']);
  assert.equal(buildDigest(once).digest, buildDigest(messy).digest, '对象键无序：键序不影响摘要');
  // 同内容不同键序 → 无差异。
  assert.deepEqual(diffTrees(messy, once), []);
});
