/**
 * 场景测试：重复段落、撤销、文本搬移、同位置插入、连续补丁重叠替换。
 * 每个场景都验证：组合映射（compose 后一次迁移）与逐步映射（逐补丁迁移）结果一致。
 */
import { Assoc, apply, compose, fromEdits, invert, toEdits, transformPosition } from '../src/patch';
import { Bias, createAnchor, migrateAnchor } from '../src/anchor';
import { assert, assertEqual, test } from './framework';
import { mulberry32, randomDoc, randomEdits } from './random';

test('场景: 重复段落 —— 删除所在段落产生候选，绝不猜选', () => {
  const para = 'the quick brown fox jumps over the lazy dog. ';
  const L = para.length;
  const doc = para + para + para;
  const pos = L + 20; // 第二段内部
  const a0 = createAnchor(doc, pos, { id: 'p', contextLength: 16 });
  assert(pos - 16 >= L && pos + 16 <= 2 * L, 'context window must fit inside one paragraph');

  // 删除第二段：剩余两段上下文完全相同 → 两个候选
  const p1 = fromEdits(doc.length, [{ start: L, end: 2 * L, text: '' }]);
  const doc1 = apply(doc, p1);
  const s1 = migrateAnchor(a0, p1, doc1);
  assertEqual(s1.result.kind, 'candidates');
  if (s1.result.kind === 'candidates') {
    assertEqual(s1.result.positions, [20, L + 20]);
  }
  assert(s1.anchor.tombstone !== null && s1.anchor.tombstone.cause === 'ambiguous',
    'ambiguous anchor keeps tombstone');

  // 再删第一段：只剩唯一匹配 → 复活到唯一位置
  const p2 = fromEdits(doc1.length, [{ start: 0, end: L, text: '' }]);
  const doc2 = apply(doc1, p2);
  const s2 = migrateAnchor(s1.anchor, p2, doc2);
  assertEqual(s2.result, { kind: 'unique', pos: 20 });
  assertEqual(s2.anchor.tombstone, null);

  // 组合补丁直接迁移，结果必须与逐步一致
  const pc = compose(p1, p2);
  assertEqual(apply(doc, pc), doc2);
  const sc = migrateAnchor(a0, pc, doc2);
  assertEqual(sc.result, { kind: 'unique', pos: 20 });

  // 删除剩余全部 → 失效，墓碑保留且后续补丁不丢失
  const p3 = fromEdits(doc2.length, [{ start: 0, end: doc2.length, text: '' }]);
  const doc3 = apply(doc2, p3);
  const s3 = migrateAnchor(s2.anchor, p3, doc3);
  assertEqual(s3.result.kind, 'invalid');
  assert(s3.anchor.tombstone !== null, 'tombstone must be kept');
  const s4 = migrateAnchor(s3.anchor, [], doc3);
  assertEqual(s4.result.kind, 'invalid');
  assert(s4.anchor.tombstone !== null, 'tombstone must survive later patches');
});

test('场景: 撤销 —— 删除覆盖后撤销，锚点回到原位置', () => {
  const doc = 'alpha beta gamma delta epsilon';
  const pos = doc.indexOf('gamma');
  const a0 = createAnchor(doc, pos, { id: 'u', contextLength: 8 });

  const p1 = fromEdits(doc.length, [{ start: 6, end: 17, text: '' }]); // 删除 'beta gamma '
  const doc1 = apply(doc, p1);
  const s1 = migrateAnchor(a0, p1, doc1);
  assertEqual(s1.result.kind, 'invalid');

  // 撤销 = 应用逆补丁
  const p2 = invert(doc, p1);
  const doc2 = apply(doc1, p2);
  assertEqual(doc2, doc);
  const s2 = migrateAnchor(s1.anchor, p2, doc2);
  assertEqual(s2.result, { kind: 'unique', pos });

  // 组合补丁是恒等变换，直接迁移结果一致
  const pc = compose(p1, p2);
  assertEqual(apply(doc, pc), doc);
  const sc = migrateAnchor(a0, pc, doc2);
  assertEqual(sc.result, { kind: 'unique', pos });
  assertEqual(
    { pos: sc.anchor.pos, left: sc.anchor.left, right: sc.anchor.right },
    { pos: s2.anchor.pos, left: s2.anchor.left, right: s2.anchor.right }
  );
});

test('场景: 文本搬移 —— 剪切后粘贴到他处，锚点跟随唯一位移', () => {
  const sentence = 'the moved sentence keeps its own surrounding context';
  const doc = 'HEADER. ' + sentence + ' MIDDLE. TAIL.';
  const start = doc.indexOf(sentence);
  const pos = start + 20;
  const a0 = createAnchor(doc, pos, { id: 'm', contextLength: 12 });

  // 剪切
  const p1 = fromEdits(doc.length, [{ start, end: start + sentence.length, text: '' }]);
  const doc1 = apply(doc, p1);
  const s1 = migrateAnchor(a0, p1, doc1);
  assertEqual(s1.result.kind, 'invalid');

  // 粘贴到文末
  const insertAt = doc1.length;
  const p2 = fromEdits(doc1.length, [{ start: insertAt, end: insertAt, text: ' ' + sentence }]);
  const doc2 = apply(doc1, p2);
  const s2 = migrateAnchor(s1.anchor, p2, doc2);
  const expectPos = insertAt + 1 + 20;
  assertEqual(s2.result, { kind: 'unique', pos: expectPos });
  assertEqual(s2.anchor.tombstone, null);

  // 组合补丁直接迁移：位置被删 → 上下文在新文档唯一 → 同一位移
  const pc = compose(p1, p2);
  assertEqual(apply(doc, pc), doc2);
  const sc = migrateAnchor(a0, pc, doc2);
  assertEqual(sc.result, { kind: 'unique', pos: expectPos });
  assertEqual(
    { pos: sc.anchor.pos, left: sc.anchor.left, right: sc.anchor.right },
    { pos: s2.anchor.pos, left: s2.anchor.left, right: s2.anchor.right }
  );
});

test('场景: 同位置插入 —— 连续补丁在同一位置插入，bias 语义一致', () => {
  const doc = 'abcdef';
  const p1 = fromEdits(doc.length, [{ start: 3, end: 3, text: 'XY' }]);
  const mid = apply(doc, p1);
  const p2 = fromEdits(mid.length, [{ start: 3, end: 3, text: 'Q' }]);
  const fin = apply(mid, p2);
  assertEqual(fin, 'abcQXYdef');

  const pc = compose(p1, p2);
  assertEqual(apply(doc, pc), fin);

  const biases: Bias[] = ['left', 'right'];
  for (const bias of biases) {
    const a0 = createAnchor(doc, 3, { id: 's-' + bias, bias, contextLength: 3 });
    const s1 = migrateAnchor(a0, p1, mid);
    const s2 = migrateAnchor(s1.anchor, p2, fin);
    const sc = migrateAnchor(a0, pc, fin);
    const expectPos = bias === 'left' ? 3 : 6;
    assertEqual(s2.result, { kind: 'unique', pos: expectPos });
    assertEqual(sc.result, s2.result, 'composed must equal stepwise for bias=' + bias);
    assertEqual(
      { pos: sc.anchor.pos, left: sc.anchor.left, right: sc.anchor.right },
      { pos: s2.anchor.pos, left: s2.anchor.left, right: s2.anchor.right }
    );
  }

  // 位置映射层面：所有位置两种亲和性，组合 == 逐步
  const assocs: Assoc[] = ['left', 'right'];
  for (let p = 0; p <= doc.length; p++) {
    for (const assoc of assocs) {
      const s = transformPosition(p, p1, assoc);
      const step = s === null ? null : transformPosition(s, p2, assoc);
      assertEqual(transformPosition(p, pc, assoc), step);
    }
  }
});

test('场景: 连续补丁重叠替换 —— 跨补丁修改前一个补丁的替换区', () => {
  const doc = '0123456789';
  const p1 = fromEdits(doc.length, [{ start: 2, end: 8, text: 'abcdef' }]);
  const mid = apply(doc, p1); // '01abcdef89'
  // 第二个补丁的替换范围落在第一个补丁刚插入的文本内部
  const p2 = fromEdits(mid.length, [{ start: 3, end: 5, text: 'WXYZ' }]);
  const fin = apply(mid, p2); // '01aWXYZdef89'
  // 第三个补丁的替换范围横跨保留区与插入区的边界
  const p3 = fromEdits(fin.length, [{ start: 0, end: 4, text: 'R' }]);
  const fin2 = apply(fin, p3); // 'RXYZdef89'

  const pc = compose(compose(p1, p2), p3);
  assertEqual(apply(doc, pc), fin2);
  assertEqual(toEdits(compose(p1, p2)), [{ start: 2, end: 8, text: 'aWXYZdef' }]);

  // 所有位置、两种亲和性：组合映射 == 逐步映射
  const assocs: Assoc[] = ['left', 'right'];
  for (let p = 0; p <= doc.length; p++) {
    for (const assoc of assocs) {
      const s1 = transformPosition(p, p1, assoc);
      const s2 = s1 === null ? null : transformPosition(s1, p2, assoc);
      const step = s2 === null ? null : transformPosition(s2, p3, assoc);
      assertEqual(transformPosition(p, pc, assoc), step, 'pos=' + p + ' assoc=' + assoc);
    }
  }

  // 锚点层面：替换区之后的锚点随补丁平移，组合与逐步一致
  const a0 = createAnchor(doc, 9, { id: 'o', contextLength: 2 });
  const t1 = migrateAnchor(a0, p1, mid);
  const t2 = migrateAnchor(t1.anchor, p2, fin);
  const t3 = migrateAnchor(t2.anchor, p3, fin2);
  const tc = migrateAnchor(a0, pc, fin2);
  assertEqual(t3.result.kind, 'unique');
  assertEqual(tc.result, t3.result);
});

test('随机不变量: 锚点层面组合迁移与逐步迁移一致（位置被保留时）', () => {
  const rnd = mulberry32(991207);
  const biases: Bias[] = ['left', 'right'];
  for (let iter = 0; iter < 200; iter++) {
    const doc = randomDoc(rnd);
    const pa = fromEdits(doc.length, randomEdits(doc.length, rnd));
    const mid = apply(doc, pa);
    const pb = fromEdits(mid.length, randomEdits(mid.length, rnd));
    const fin = apply(mid, pb);
    const pc = compose(pa, pb);
    for (let k = 0; k < 3; k++) {
      const pos = doc.length === 0 ? 0 : Math.floor(rnd() * (doc.length + 1));
      const bias = biases[Math.floor(rnd() * 2)];
      // 只比较位置全程被补丁保留的情形（墓碑/复活路径另有场景测试覆盖）
      if (transformPosition(pos, pa, bias) === null) continue;
      if (transformPosition(pos, pc, bias) === null) continue;
      const a0 = createAnchor(doc, pos, { bias, contextLength: 4 });
      const s1 = migrateAnchor(a0, pa, mid);
      const s2 = migrateAnchor(s1.anchor, pb, fin);
      const sc = migrateAnchor(a0, pc, fin);
      assertEqual(s1.result.kind, 'unique');
      assertEqual(s2.result.kind, 'unique');
      assertEqual(sc.result, s2.result, 'iter=' + iter + ' pos=' + pos + ' bias=' + bias);
      assertEqual(
        { pos: sc.anchor.pos, left: sc.anchor.left, right: sc.anchor.right },
        { pos: s2.anchor.pos, left: s2.anchor.left, right: s2.anchor.right }
      );
    }
  }
});
