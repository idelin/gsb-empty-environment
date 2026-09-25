import test from 'node:test';
import assert from 'node:assert/strict';
import { diffTrees } from '../src/diff.ts';
import { applyPatch, invertPatch } from '../src/patch.ts';
import { buildDigest } from '../src/digest.ts';
import { DigestMismatchError, PatchApplyError } from '../src/errors.ts';
import { deepClone } from '../src/types.ts';

test('补丁任一步失败 → 回滚所有数组下标与根', () => {
  const original = { arr: [1, 2, 3], meta: { n: 3 }, tag: 'kept' };
  const root = deepClone(original);
  const patch = {
    ops: [
      { op: 'set', path: ['arr', 0], value: 10 }, // 改下标
      { op: 'insert', path: ['arr', 1], value: 99 }, // 移动全部后续下标
      { op: 'set', path: ['meta', 'n'], value: 4 },
      { op: 'remove', path: ['arr', 99] }, // 失败：下标越界
    ],
  } as const;

  assert.throws(
    () => applyPatch(root, patch as never),
    (e: unknown) =>
      e instanceof PatchApplyError && assert.deepEqual(e.path, ['arr', 99]) === undefined && e.opIndex === 3,
  );
  assert.deepEqual(root, original, '失败后所有下标与内容必须恢复');

  // 根被整体替换后失败，同样回滚。
  const root2 = deepClone(original);
  const patch2 = {
    ops: [
      { op: 'set', path: [], value: { replaced: true } },
      { op: 'remove', path: ['missing', 'key'] },
    ],
  } as const;
  assert.throws(() => applyPatch(root2, patch2 as never), PatchApplyError);
  assert.deepEqual(root2, original, '根替换失败也应恢复原根');
});

test('逆补丁还原', () => {
  const base = { a: [{ id: 'x', v: 1 }, { id: 'y', v: 2 }], b: { c: 1 }, gone: 'x' };
  const target = { a: [{ id: 'y', v: 2 }, { id: 'x', v: 9 }, { id: 'z', v: 0 }], b: { c: 2 } };

  const ops = diffTrees(base, target, { arrayMode: 'id' });
  const after = applyPatch(deepClone(base), { ops });
  assert.deepEqual(after, target, '正向补丁应得到目标');

  const inverse = invertPatch({ ops }, base);
  const restored = applyPatch(deepClone(target), inverse);
  assert.deepEqual(restored, base, '逆补丁应还原基线');
});

test('旧基线与目标摘要校验先于修改', () => {
  const root = { a: 1 };
  const baseDigest = buildDigest(root).digest;
  const patch = { ops: [{ op: 'set', path: ['a'], value: 2 }] } as const;

  // 基线摘要不符：抛错且树完全未被修改。
  assert.throws(
    () => applyPatch(root, patch as never, { expectBaseDigest: 'deadbeef' }),
    (e: unknown) => e instanceof DigestMismatchError && assert.deepEqual(e.path, []) === undefined,
  );
  assert.deepEqual(root, { a: 1 });

  // 目标摘要不符：应用后校验失败并回滚。
  assert.throws(
    () => applyPatch(root, patch as never, { expectBaseDigest: baseDigest, expectTargetDigest: 'deadbeef' }),
    DigestMismatchError,
  );
  assert.deepEqual(root, { a: 1 }, '目标摘要校验失败应回滚');

  // 摘要正确则通过。
  const out = applyPatch(root, patch as never, {
    expectBaseDigest: baseDigest,
    expectTargetDigest: buildDigest({ a: 2 }).digest,
  });
  assert.deepEqual(out, { a: 2 });
});
