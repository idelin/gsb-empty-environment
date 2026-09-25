import test from 'node:test';
import assert from 'node:assert/strict';
import { ChunkStore, diffChunked } from '../src/chunked.ts';
import { applyPatch } from '../src/patch.ts';
import { diffTrees } from '../src/diff.ts';
import { merge3 } from '../src/merge.ts';
import { DuplicateArrayIdError } from '../src/errors.ts';
import { deepClone, type JsonValue } from '../src/types.ts';

/** 3×3×3 深层树：块深 2 → 块位于深度 0/2/4，共 1+9+27=37 块。 */
function bigTree(): JsonValue {
  const out: Record<string, JsonValue> = {};
  for (let k = 0; k < 3; k++) {
    const mid: Record<string, JsonValue> = {};
    for (let m = 0; m < 3; m++) {
      const leaf: Record<string, JsonValue> = {};
      for (let l = 0; l < 3; l++) leaf[`l${l}`] = { v: 0 };
      mid[`m${m}`] = leaf;
    }
    out[`k${k}`] = mid;
  }
  return out;
}

test('统计实际读取块以验证摘要裁剪', () => {
  const a = bigTree();
  const b = deepClone(a);
  ((b as never as Record<string, Record<string, Record<string, { v: number }>>>)['k1']['m2']['l1']).v = 42;

  const sa = ChunkStore.fromTree(a, 2);
  const sb = ChunkStore.fromTree(b, 2);
  assert.equal(sa.totalBlocks, 37);

  const ops = diffChunked(sa, sb);
  assert.deepEqual(ops, [{ op: 'set', path: ['k1', 'm2', 'l1', 'v'], value: 42, oldValue: 0 }]);
  assert.equal(sa.reads, 3, '只应读取变更路径上的 根/中间/叶子 三块');
  assert.equal(sb.reads, 3);
  assert.ok(sa.reads < sa.totalBlocks, '读取块数应远小于总块数（摘要裁剪生效）');

  // 未变更树：零读取。
  const s1 = ChunkStore.fromTree(a, 2);
  const s2 = ChunkStore.fromTree(deepClone(a), 2);
  assert.deepEqual(diffChunked(s1, s2), []);
  assert.equal(s1.reads, 0);
});

test('跨块子树移动', () => {
  const sub = { x: 1, y: [1, 2, 3] };
  const a = { p: { sub: deepClone(sub) }, q: {} };
  const b = { p: {}, q: { sub: deepClone(sub) } }; // sub 跨块从 p 移到 q

  const sa = ChunkStore.fromTree(a, 2);
  const sb = ChunkStore.fromTree(b, 2);
  const ops = diffChunked(sa, sb);
  assert.deepEqual(ops, [{ op: 'move', from: ['p', 'sub'], to: ['q', 'sub'] }], '应识别为移动而非删除+新增');
  assert.equal(sa.reads, 1, '被移动子树的块无需读取（摘要比对即可）');

  const out = applyPatch(deepClone(a), { ops });
  assert.deepEqual(out, b);
});

test('重复数组ID报错并定位到数组路径', () => {
  const dup = { items: [{ id: 'x', v: 1 }, { id: 'x', v: 2 }] };
  const other = { items: [] };

  assert.throws(
    () => diffTrees(dup, other, { arrayMode: 'id' }),
    (e: unknown) =>
      e instanceof DuplicateArrayIdError && e.id === 'x' && assert.deepEqual(e.path, ['items']) === undefined,
  );
  assert.throws(
    () => merge3(dup, other, other, { arrayMode: 'id' }),
    (e: unknown) => e instanceof DuplicateArrayIdError && assert.deepEqual(e.path, ['items']) === undefined,
  );
});
