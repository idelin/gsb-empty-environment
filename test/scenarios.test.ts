import * as assert from 'assert';
import {
  AnchorState,
  Patch,
  applyPatch,
  composePatches,
  createAnchor,
  mapPosition,
  migrateAnchor,
  migrateThroughPatches,
  resolveByContext,
} from '../src';
import { test } from './harness';

/** 验证「组合映射 ≡ 逐步映射」：同一个初始文档与锚点，两条路径必须产出完全一致的文档与锚点状态。 */
function assertConsistent(
  doc0: string,
  patches: Patch[],
  anchor: AnchorState
): { doc: string; state: AnchorState } {
  const stepwise = migrateThroughPatches(doc0, patches, anchor);
  const composed = migrateAnchor(doc0, composePatches(...patches), anchor);
  assert.strictEqual(composed.doc, stepwise.doc, 'composed doc !== stepwise doc');
  assert.deepStrictEqual(composed.state, stepwise.state, 'composed state !== stepwise state');
  return stepwise;
}

test('连续补丁允许重叠替换（补丁内与补丁间）', () => {
  const doc = 'abcdefgh';
  const p1: Patch = { edits: [{ start: 2, end: 6, text: 'XYZW' }] };
  // p2 的坐标落在 p1 刚插入的文本内部（重叠替换）
  const p2: Patch = { edits: [{ start: 3, end: 5, text: 'q' }] };
  assert.strictEqual(applyPatch(doc, p1), 'abXYZWgh');
  assert.strictEqual(applyPatch(applyPatch(doc, p1), p2), 'abXqWgh');
  // 同一补丁内顺序重叠替换，结果相同
  const combined: Patch = { edits: [{ start: 2, end: 6, text: 'XYZW' }, { start: 3, end: 5, text: 'q' }] };
  assert.strictEqual(applyPatch(doc, combined), 'abXqWgh');
  assert.strictEqual(applyPatch(doc, composePatches(p1, p2)), 'abXqWgh');
});

test('位置映射边界规则与同位置插入', () => {
  // 同位置插入：锚点保持在插入文本之前（左亲和），结果确定
  assert.deepStrictEqual(mapPosition(6, { edits: [{ start: 6, end: 6, text: 'big ' }] }), {
    kind: 'mapped',
    position: 6,
  });
  // 删除区间内部：墓碑
  assert.deepStrictEqual(mapPosition(8, { edits: [{ start: 6, end: 10, text: '' }] }), {
    kind: 'tombstone',
  });
  // 删除区间左右边界：幸存
  assert.deepStrictEqual(mapPosition(6, { edits: [{ start: 6, end: 10, text: '' }] }), {
    kind: 'mapped',
    position: 6,
  });
  assert.deepStrictEqual(mapPosition(10, { edits: [{ start: 6, end: 10, text: 'xy' }] }), {
    kind: 'mapped',
    position: 8,
  });
  // 组合映射 ≡ 逐步映射（纯位置层面）
  const p1: Patch = { edits: [{ start: 6, end: 6, text: 'big ' }] };
  const p2: Patch = { edits: [{ start: 6, end: 6, text: 'wide ' }] };
  const step = mapPosition(6, p2);
  const both = mapPosition(6, composePatches(p1, p2));
  const first = mapPosition(6, p1);
  assert.deepStrictEqual(first, { kind: 'mapped', position: 6 });
  assert.deepStrictEqual(step, both);
});

test('同位置插入：跨补丁一致且锚点幸存', () => {
  const doc = 'hello world';
  const anchor = createAnchor(doc, 6, 3);
  const p1: Patch = { edits: [{ start: 6, end: 6, text: 'big ' }] };
  const p2: Patch = { edits: [{ start: 6, end: 6, text: 'wide ' }] };
  const { doc: out, state } = assertConsistent(doc, [p1, p2], anchor);
  assert.strictEqual(out, 'hello wide big world');
  assert.strictEqual(state.status, 'resolved');
  assert.strictEqual(state.position, 6);
  assert.strictEqual(state.tombstones.length, 0);
});

test('重复段落：编辑他处时锚点按连续性迁移', () => {
  const para = 'foo bar\n';
  const doc = para + para + para;
  const anchor = createAnchor(doc, 12, 4); // 第二段 "foo " 与 "bar" 之间
  const p: Patch = { edits: [{ start: 0, end: 0, text: 'X' }] };
  const { state } = assertConsistent(doc, [p], anchor);
  assert.strictEqual(state.status, 'resolved');
  assert.strictEqual(state.position, 13);
});

test('重复段落：删除锚点所在段落产生多个候选，绝不猜选', () => {
  const para = 'foo bar\n';
  const doc = para + para + para;
  const anchor = createAnchor(doc, 12, 4);
  const p1: Patch = { edits: [{ start: 8, end: 16, text: '' }] }; // 删除第二段
  const undo: Patch = { edits: [{ start: 8, end: 8, text: para }] };
  const { doc: out, state } = assertConsistent(doc, [p1, undo], anchor);
  assert.strictEqual(out, doc);
  // 删除后剩两个相同段落：候选而非唯一位置
  const mid = migrateThroughPatches(doc, [p1], anchor);
  assert.strictEqual(mid.state.status, 'ambiguous');
  assert.deepStrictEqual(mid.state.candidates, [4, 12]);
  assert.strictEqual(mid.state.tombstones.length, 1);
  assert.strictEqual(mid.state.tombstones[0].deletedText, para);
  // 撤销后三个段落完全相同：仍然只能给候选，不能猜选
  assert.strictEqual(state.status, 'ambiguous');
  assert.deepStrictEqual(state.candidates, [4, 12, 20]);
  assert.strictEqual(state.tombstones.length, 1);
});

test('撤销：删除覆盖锚点保留墓碑，撤销后按唯一上下文恢复', () => {
  const doc = 'alpha beta gamma';
  const anchor = createAnchor(doc, 8, 3); // "beta" 内部
  const p1: Patch = { edits: [{ start: 6, end: 10, text: '' }] }; // 删除 "beta"
  const p2: Patch = { edits: [{ start: 6, end: 6, text: 'beta' }] }; // 撤销
  const mid = migrateThroughPatches(doc, [p1], anchor);
  assert.strictEqual(mid.doc, 'alpha  gamma');
  assert.strictEqual(mid.state.status, 'tombstone');
  assert.deepStrictEqual(mid.state.tombstones, [{ position: 8, deletedText: 'beta' }]);
  const { doc: out, state } = assertConsistent(doc, [p1, p2], anchor);
  assert.strictEqual(out, doc);
  assert.strictEqual(state.status, 'resolved');
  assert.strictEqual(state.position, 8);
  // 墓碑永久保留
  assert.deepStrictEqual(state.tombstones, [{ position: 8, deletedText: 'beta' }]);
});

test('文本搬移：同一补丁内删除加插入，锚点跟随唯一上下文', () => {
  const doc = 'one two three';
  const anchor = createAnchor(doc, 5, 3); // "two" 内部
  const move: Patch = {
    edits: [
      { start: 4, end: 8, text: '' }, // 删除 "two "
      { start: 9, end: 9, text: ' two ' }, // 在文末重新插入
    ],
  };
  const { doc: out, state } = assertConsistent(doc, [move], anchor);
  assert.strictEqual(out, 'one three two ');
  assert.strictEqual(state.status, 'resolved');
  assert.strictEqual(state.position, 11);
  assert.strictEqual(state.tombstones.length, 1);
  assert.strictEqual(state.tombstones[0].deletedText, 'two ');
});

test('文本搬移：拆成两个补丁与合并补丁结果一致', () => {
  const doc = 'one two three';
  const anchor = createAnchor(doc, 5, 3);
  const del: Patch = { edits: [{ start: 4, end: 8, text: '' }] };
  const ins: Patch = { edits: [{ start: 9, end: 9, text: ' two ' }] };
  const mid = migrateThroughPatches(doc, [del], anchor);
  assert.strictEqual(mid.state.status, 'tombstone'); // 中间版本中文本暂缺
  const { doc: out, state } = assertConsistent(doc, [del, ins], anchor);
  assert.strictEqual(out, 'one three two ');
  assert.strictEqual(state.status, 'resolved');
  assert.strictEqual(state.position, 11);
});

test('resolveByContext：唯一、多候选、失效三种输出', () => {
  const doc = 'x foo y foo z';
  const base = createAnchor('foo', 1, 2); // 仅借用其 anchor 结构
  const state: AnchorState = {
    ...base,
    anchor: { left: ' ', right: 'foo', radius: 3 },
  };
  // 多候选
  const ambiguous = resolveByContext(doc, state);
  assert.strictEqual(ambiguous.status, 'ambiguous');
  assert.deepStrictEqual(ambiguous.candidates, [2, 8]);
  // 唯一
  const unique = resolveByContext('x foo y', state);
  assert.strictEqual(unique.status, 'resolved');
  assert.strictEqual(unique.position, 2);
  // 失效
  const gone = resolveByContext('nothing here', state);
  assert.strictEqual(gone.status, 'invalidated');
});

test('随机编辑序列：组合映射与逐步映射一致（文档、位置、锚点状态）', () => {
  // 确定性伪随机数
  let seed = 0x2f6e2b1;
  const rand = (n: number): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed % n;
  };
  const alphabet = ['a', 'b', 'c', ' ', '\n'];
  for (let iter = 0; iter < 300; iter += 1) {
    let doc = '';
    const docLen = 5 + rand(30);
    for (let i = 0; i < docLen; i += 1) {
      doc += alphabet[rand(alphabet.length)];
    }
    const patches: Patch[] = [];
    let cur = doc;
    for (let pi = 0; pi < 3; pi += 1) {
      const edits = [];
      const editCount = 1 + rand(4);
      for (let ei = 0; ei < editCount; ei += 1) {
        const start = rand(cur.length + 1);
        const end = start + rand(cur.length - start + 1);
        let text = '';
        const textLen = rand(4);
        for (let ti = 0; ti < textLen; ti += 1) {
          text += alphabet[rand(alphabet.length)];
        }
        edits.push({ start, end, text });
        cur = cur.slice(0, start) + text + cur.slice(end);
      }
      patches.push({ edits });
    }
    const anchor = createAnchor(doc, rand(doc.length + 1), 3);
    // 文档层面
    const stepDoc = migrateThroughPatches(doc, patches, anchor).doc;
    const compDoc = applyPatch(doc, composePatches(...patches));
    assert.strictEqual(compDoc, stepDoc, `iter ${iter}: doc mismatch`);
    // 锚点状态层面
    assertConsistent(doc, patches, anchor);
  }
});
