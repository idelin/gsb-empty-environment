import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  apply,
  compose,
  digest,
  invert,
  makePatch,
  relocate,
  transform,
  validateCommutation,
  splitsSurrogatePair,
  ComposeError,
  ConflictError,
  ContextMismatchError,
  DigestMismatchError,
  SurrogateBoundaryError,
  type Op,
  type Patch,
} from '../src/patch.ts';

/** 确定性 id 生成器，便于断言 */
function seqIds(prefix = 'op'): () => string {
  let n = 0;
  return () => `${prefix}-${++n}`;
}

/** 逐步编辑参考实现：一个 op 一次字符串切片，独立校验组合/求逆结果 */
function referenceApply(base: string, ops: Op[]): string {
  let doc = base;
  let pos = 0;
  for (const op of ops) {
    if (op.kind === 'retain') {
      pos += op.count;
    } else if (op.kind === 'delete') {
      assert.equal(doc.slice(pos, pos + op.count), op.removed, 'reference delete context');
      doc = doc.slice(0, pos) + doc.slice(pos + op.count);
    } else {
      doc = doc.slice(0, pos) + op.text + doc.slice(pos);
      pos += op.text.length;
    }
  }
  return doc;
}

/** 手工构造补丁（自动补全摘要），用于变换/冲突等场景 */
function patchFromOps(base: string, ops: Op[]): Patch {
  const result = referenceApply(base, ops);
  return {
    baseDigest: digest(base),
    baseLength: base.length,
    resultDigest: digest(result),
    resultLength: result.length,
    ops,
  };
}

test('apply 与逐步参考一致：基本增删改（LF 与 CRLF）', () => {
  for (const nl of ['\n', '\r\n']) {
    const base = `hello${nl}brave new${nl}world${nl}`;
    const edited = `hello${nl}BRAVE new${nl}world!${nl}`;
    const p = makePatch(base, edited, seqIds());
    assert.equal(apply(base, p), edited);
    assert.equal(referenceApply(base, p.ops), edited);
  }
});

test('UTF-16 偏移：代理对占两个码元，且不得切开代理对', () => {
  const base = 'a😀b'; // 😀 = U+1F600，占 2 个 UTF-16 码元
  assert.equal(base.length, 4);
  const p = makePatch(base, 'a😀Xb', seqIds());
  assert.deepEqual(
    p.ops.map((o) => (o.kind === 'retain' ? o.count : o.kind)),
    [3, 'insert', 1],
  );
  assert.equal(apply(base, p), 'a😀Xb');

  // 手工构造在代理对中间（pos=2）切开的补丁 → 必须拒绝
  const evil = patchFromOps(base, [
    { kind: 'retain', count: 2 },
    { kind: 'insert', text: 'X', id: 'x' },
    { kind: 'retain', count: 2 },
  ]);
  assert.throws(() => apply(base, evil), SurrogateBoundaryError);
  assert.ok(splitsSurrogatePair(base, 2));
  assert.ok(!splitsSurrogatePair(base, 3));
});

test('makePatch 的公共前后缀不会切开代理对', () => {
  const base = 'A😀';
  const edited = 'A\uD83D'; // 截掉低代理项：公共前缀若不回退会切开代理对
  const p = makePatch(base, edited, seqIds());
  assert.equal(apply(base, p), edited);
  const p2 = makePatch(edited, base, seqIds());
  assert.equal(apply(edited, p2), base);
});

test('摘要校验：长度相同但内容不同的基线不能通过', () => {
  const base = 'hello world';
  const p = makePatch(base, 'hello WORLD', seqIds());
  const sameLengthDifferent = 'hxllo world'; // 同为 11 码元
  assert.equal(sameLengthDifferent.length, base.length);
  assert.throws(() => apply(sameLengthDifferent, p), DigestMismatchError);
  assert.throws(() => apply('hello worl', p), DigestMismatchError); // 长度不同
});

test('失败原子性：任一 op 校验失败即抛错，不产生部分结果', () => {
  const base = 'foo bar foo bar foo';
  const good = makePatch(base, 'foo BAR foo bar foo', seqIds());
  assert.equal(apply(base, good), 'foo BAR foo bar foo');

  // 篡改第二个 delete 的上下文原文
  const tampered: Patch = {
    ...good,
    ops: [
      { kind: 'retain', count: 4 },
      { kind: 'delete', count: 3, removed: 'XYZ' }, // 实际为 'bar'
      { kind: 'insert', text: 'BAR', id: 'i1' },
      { kind: 'retain', count: 12 },
    ],
  };
  assert.throws(() => apply(base, tampered), ContextMismatchError);
  assert.equal(base, 'foo bar foo bar foo'); // 原文不受影响

  // 前几个 op 合法、靠后的 op 失配：同样整体失败
  const lateFail: Patch = {
    baseDigest: digest(base),
    baseLength: base.length,
    ops: [
      { kind: 'delete', count: 3, removed: 'foo' },
      { kind: 'retain', count: 5 },
      { kind: 'delete', count: 3, removed: 'ZZZ' }, // 实际为 'foo'
      { kind: 'retain', count: 8 },
    ],
  };
  assert.throws(() => apply(base, lateFail), ContextMismatchError);
  assert.equal(base, 'foo bar foo bar foo');
});

test('P 再逆 P 恢复逐字节原文（含 CRLF 与 emoji）', () => {
  const cases = [
    'plain ascii text',
    'line1\r\nline2 😀 end\nline3\n',
    'foo bar foo bar foo',
    '😀😀😀\r\n',
    '',
  ];
  for (const base of cases) {
    const edited = base.length ? base.slice(0, 1) + 'INSERTED ' + base.slice(1) : 'INSERTED';
    const p = makePatch(base, edited, seqIds());
    const restored = apply(apply(base, p), invert(p, seqIds('inv')));
    assert.equal(restored, base);
    assert.ok(Buffer.from(restored, 'utf8').equals(Buffer.from(base, 'utf8')), 'byte-exact');
  }
});

test('组合等于顺序应用；compose(P, invert(P)) 为恒等', () => {
  const bases = ['foo bar foo bar foo', 'a\r\nb\r\nc\r\n', 'x😀y😀z'];
  for (const base of bases) {
    const mid = base + ' <<tail';
    const final = 'head>> ' + mid.replaceAll('foo', 'qux');
    const P = makePatch(base, mid, seqIds('p'));
    const Q = makePatch(mid, final, seqIds('q'));
    const PQ = compose(P, Q);
    assert.equal(apply(base, PQ), apply(apply(base, P), Q));
    assert.equal(apply(base, PQ), referenceApply(base, PQ.ops));

    const identity = compose(P, invert(P, seqIds('i')));
    assert.ok(identity.ops.every((o) => o.kind === 'retain'), 'inverse ops fully cancel');
    assert.equal(apply(base, identity), base);

    // 替换型补丁（delete+insert 混合）与其逆组合同样归约为恒等
    const R = makePatch(base, 'REPLACED|' + base.slice(2) + '|TAIL', seqIds('r'));
    const rid = compose(R, invert(R, seqIds('ir')));
    assert.ok(rid.ops.every((o) => o.kind === 'retain'), 'replace-type inverse cancels');
    assert.equal(apply(base, rid), base);
  }
});

test('组合器消除互逆操作时保留后续操作的坐标来源', () => {
  const base = 'abc';
  // P: 在 1 处插入 'XY' → 'aXYbc'
  const P = patchFromOps(base, [
    { kind: 'retain', count: 1 },
    { kind: 'insert', text: 'XY', id: 'ins-1' },
    { kind: 'retain', count: 2 },
  ]);
  // Q: 删除 'XY'（引用其来源身份），并在末尾追加 'Z' → 'abcZ'
  const Q = patchFromOps('aXYbc', [
    { kind: 'retain', count: 1 },
    { kind: 'delete', count: 2, removed: 'XY', removedId: 'ins-1' },
    { kind: 'retain', count: 2 },
    { kind: 'insert', text: 'Z', id: 'ins-2' },
  ]);
  const PQ = compose(P, Q);
  // 互逆对已消除，剩余坐标仍指向基线：retain(3) + insert Z
  assert.deepEqual(PQ.ops, [
    { kind: 'retain', count: 3 },
    { kind: 'insert', text: 'Z', id: 'ins-2' },
  ]);
  assert.equal(apply(base, PQ), 'abcZ');
});

test('组合时身份不符的 delete 不能消除 insert', () => {
  const base = 'abc';
  const P = patchFromOps(base, [
    { kind: 'retain', count: 1 },
    { kind: 'insert', text: 'XY', id: 'ins-1' },
    { kind: 'retain', count: 2 },
  ]);
  const Q = patchFromOps('aXYbc', [
    { kind: 'retain', count: 1 },
    { kind: 'delete', count: 2, removed: 'XY', removedId: 'someone-else' },
    { kind: 'retain', count: 2 },
  ]);
  assert.throws(() => compose(P, Q), ComposeError);
});

test('操作身份：同文本的并发插入不会被误认作同一次编辑', () => {
  const base = 'xy';
  const P = patchFromOps(base, [
    { kind: 'insert', text: 'foo', id: 'a-insert' },
    { kind: 'retain', count: 2 },
  ]);
  const Q = patchFromOps(base, [
    { kind: 'insert', text: 'foo', id: 'b-insert' },
    { kind: 'retain', count: 2 },
  ]);
  validateCommutation(P, Q, base); // 不冲突、不合并
  const merged1 = apply(apply(base, Q), transform(P, Q, base));
  assert.equal(merged1, 'foofooxy'); // 按 id 字典序：a-insert 在前
  assert.equal(apply(apply(base, P), transform(Q, P, base)), 'foofooxy');
});

test('不相交编辑满足交换图；坐标随并发插入重定位', () => {
  const base = 'hello brave world\r\nsecond line 😀\r\n';
  const P = makePatch(base, base.replace('hello', 'HELLO'), seqIds('p'));
  const Q = makePatch(base, base.replace('second', 'SECOND'), seqIds('q'));
  validateCommutation(P, Q, base);
  const viaPQ = apply(apply(base, P), transform(Q, P, base));
  const viaQP = apply(apply(base, Q), transform(P, Q, base));
  assert.equal(viaPQ, viaQP);
  assert.ok(viaPQ.includes('HELLO') && viaPQ.includes('SECOND'));

  // 三方重定位：删除 'cd' 的补丁重定位到并发插入 'ZZ' 之后
  const b2 = 'abcde';
  const del = makePatch(b2, 'abe', seqIds('d')); // 删除 [2,4) 'cd'
  const ins = makePatch(b2, 'aZZbcde', seqIds('i'));
  const delRelocated = relocate(del, ins, b2);
  assert.equal(apply(apply(b2, ins), delRelocated), 'aZZbe');
  validateCommutation(del, ins, b2);
});

test('交叉删除返回冲突；不相交删除可交换', () => {
  const base = 'hello world';
  const P = patchFromOps(base, [
    { kind: 'delete', count: 5, removed: 'hello' },
    { kind: 'retain', count: 6 },
  ]);
  const Q = patchFromOps(base, [
    { kind: 'retain', count: 4 },
    { kind: 'delete', count: 2, removed: 'o ' },
    { kind: 'retain', count: 5 },
  ]);
  assert.throws(() => transform(P, Q, base), ConflictError);
  assert.throws(() => validateCommutation(P, Q, base), ConflictError);

  // 不相交删除：交换图成立
  const R = patchFromOps(base, [
    { kind: 'retain', count: 6 },
    { kind: 'delete', count: 5, removed: 'world' },
  ]);
  const S = patchFromOps(base, [
    { kind: 'delete', count: 2, removed: 'he' },
    { kind: 'retain', count: 9 },
  ]);
  validateCommutation(R, S, base);
  assert.equal(apply(apply(base, R), transform(S, R, base)), 'llo ');
});

test('重复文本与换行变更：组合/求逆/原子性对照逐步参考', () => {
  const scenarios: Array<[string, string, string]> = [
    ['foo bar foo bar foo', 'foo bar X foo bar foo', 'foo bar X foo bar'], // 重复文本中插入再删除
    ['a\nb\nc\n', 'a\nb\n', 'a\r\nb\r\n'], // 删行 + LF→CRLF
    ['one\r\ntwo\r\nthree\r\n', 'one\r\nTWO\r\nthree\r\n', 'one\r\nTWO\r\n'], // CRLF 行编辑与删行
  ];
  for (const [base, mid, final] of scenarios) {
    const P = makePatch(base, mid, seqIds('p'));
    const Q = makePatch(mid, final, seqIds('q'));

    // 应用与逐步参考一致
    assert.equal(apply(base, P), referenceApply(base, P.ops));
    assert.equal(apply(mid, Q), referenceApply(mid, Q.ops));

    // 组合 == 顺序应用 == 参考
    const PQ = compose(P, Q);
    assert.equal(apply(base, PQ), final);
    assert.equal(referenceApply(base, PQ.ops), final);

    // 求逆逐步恢复
    assert.equal(apply(apply(base, P), invert(P, seqIds('ip'))), base);
    assert.equal(apply(apply(base, PQ), invert(PQ, seqIds('ipq'))), base);

    // 失败原子性：篡改组合补丁中 delete 的上下文 → 抛错且原文不变
    const corrupted: Patch = {
      ...PQ,
      ops: PQ.ops.map((o) => (o.kind === 'delete' ? { ...o, removed: '###' } : o)),
    };
    if (corrupted.ops.some((o) => o.kind === 'delete')) {
      assert.throws(() => apply(base, corrupted), ContextMismatchError);
      assert.equal(base.length > 0, true);
    }
  }
});

test('结果摘要自检：篡改结果摘要的补丁被拒绝', () => {
  const base = 'abcdef';
  const p = makePatch(base, 'abcXYZ', seqIds());
  const bad: Patch = { ...p, resultDigest: digest('abcXYQ') };
  assert.throws(() => apply(base, bad), DigestMismatchError);
});
