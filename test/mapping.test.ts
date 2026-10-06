import test from 'node:test';
import assert from 'node:assert/strict';
import {
  composeMappings,
  queryOutput,
  querySource,
  type Fragment,
} from '../src/mapping.ts';
import {
  TaggedText,
  applyOps,
  expectedForward,
  expectedReverse,
  type Op,
} from './tagged-model.ts';

const copy = (start: number, end: number): Op => ({ type: 'copy', start, end });
const replace = (start: number, end: number, text: string): Op => ({ type: 'replace', start, end, text });
const insert = (pos: number, text: string): Op => ({ type: 'insert', pos, text });

test('替换：块原子对应，部分命中返回整个来源区间', () => {
  const t0 = TaggedText.source('hello world');
  const r1 = applyOps(t0, [copy(0, 6), replace(6, 11, 'brave new world')]);
  assert.equal(r1.text.text, 'hello brave new world');
  const r2 = applyOps(r1.text, [copy(0, 21)]);
  const m = composeMappings(r1.mapping, r2.mapping);

  assert.deepEqual(querySource(m, 0, 6), [{ start: 0, end: 6 }]);
  assert.deepEqual(querySource(m, 6, 21), [{ start: 6, end: 11 }]);
  assert.deepEqual(querySource(m, 8, 9), [{ start: 6, end: 11 }], '块内单字符 → 整个被替换区间');
  assert.deepEqual(queryOutput(m, 8, 9), [{ start: 6, end: 21 }], '反向部分命中 → 整个替换块输出');
  assert.deepEqual(queryOutput(m, 0, 5), [{ start: 0, end: 5 }]);
});

test('等长替换仍按块处理（段类型不靠长度推断）', () => {
  const t0 = TaggedText.source('abcd');
  const r1 = applyOps(t0, [replace(1, 3, 'XY')]);
  const r2 = applyOps(r1.text, [copy(0, 2)]);
  const m = composeMappings(r1.mapping, r2.mapping);

  assert.deepEqual(querySource(m, 0, 1), [{ start: 1, end: 3 }]);
  assert.deepEqual(querySource(m, 1, 2), [{ start: 1, end: 3 }]);
  assert.deepEqual(queryOutput(m, 2, 3), [{ start: 0, end: 2 }]);
});

test('纯插入：零宽锚点经两轮组合后保持锚定', () => {
  const t0 = TaggedText.source('hi');
  const r1 = applyOps(t0, [copy(0, 2), insert(2, '!')]);
  assert.equal(r1.text.text, 'hi!');
  const r2 = applyOps(r1.text, [copy(0, 3), insert(3, '?')]);
  assert.equal(r2.text.text, 'hi!?');
  const m = composeMappings(r1.mapping, r2.mapping);

  assert.deepEqual(querySource(m, 2, 3), [{ start: 2, end: 2 }], "'!' 锚定在 text0 的 2");
  assert.deepEqual(querySource(m, 3, 4), [{ start: 2, end: 2 }], "'?' 经组合仍锚定在 2");
  assert.deepEqual(
    querySource(m, 0, 4),
    [
      { start: 0, end: 2 },
      { start: 2, end: 2 },
    ],
    '零宽锚点保持独立，不并入相邻片段',
  );
  assert.deepEqual(queryOutput(m, 0, 2), [{ start: 0, end: 2 }], '半开区间不含锚点 2');
  assert.deepEqual(queryOutput(m, 2, 2), [{ start: 2, end: 4 }], '零宽查询命中两个插入段的输出');
});

test('复制：同一来源复制多份，反向查询多值', () => {
  const t0 = TaggedText.source('ab');
  const r1 = applyOps(t0, [copy(0, 2), insert(2, '-'), copy(0, 2)]);
  assert.equal(r1.text.text, 'ab-ab');
  const r2 = applyOps(r1.text, [copy(0, 5)]);
  const m = composeMappings(r1.mapping, r2.mapping);

  assert.deepEqual(querySource(m, 0, 2), [{ start: 0, end: 2 }]);
  assert.deepEqual(querySource(m, 3, 5), [{ start: 0, end: 2 }]);
  assert.deepEqual(queryOutput(m, 0, 2), [
    { start: 0, end: 2 },
    { start: 3, end: 5 },
  ]);
  assert.deepEqual(queryOutput(m, 1, 2), [
    { start: 1, end: 2 },
    { start: 4, end: 5 },
  ]);
});

test('删除空隙：正向片段不跨空隙合并，相接片段才合并', () => {
  const t0 = TaggedText.source('abcde');
  const g1 = applyOps(t0, [copy(0, 2), copy(3, 5)]); // 删除 'c'
  const g2 = applyOps(g1.text, [copy(0, 4)]);
  const withGap = composeMappings(g1.mapping, g2.mapping);
  assert.deepEqual(querySource(withGap, 0, 4), [
    { start: 0, end: 2 },
    { start: 3, end: 5 },
  ]);

  const c1 = applyOps(t0, [copy(0, 2), copy(2, 5)]); // 两段相接，无删除
  const c2 = applyOps(c1.text, [copy(0, 5)]);
  const contiguous = composeMappings(c1.mapping, c2.mapping);
  assert.deepEqual(querySource(contiguous, 0, 5), [{ start: 0, end: 5 }]);
});

test('替换跨过删除空隙：组合产生多值段，空隙保持断开', () => {
  const t0 = TaggedText.source('abcdef');
  const r1 = applyOps(t0, [copy(0, 2), copy(4, 6)]); // 删除 [2,4)，text1 = 'abef'
  const r2 = applyOps(r1.text, [replace(0, 4, 'W')]); // 整体替换为 'W'
  const m = composeMappings(r1.mapping, r2.mapping);

  assert.equal(r2.text.text, 'W');
  assert.deepEqual(querySource(m, 0, 1), [
    { start: 0, end: 2 },
    { start: 4, end: 6 },
  ]);
  assert.deepEqual(queryOutput(m, 1, 5), [{ start: 0, end: 1 }]);
});

test('两轮替换叠加 + 插入 + 复制的组合场景', () => {
  const t0 = TaggedText.source('hello world');
  const r1 = applyOps(t0, [
    copy(0, 6), // 'hello '
    replace(6, 11, 'there'), // 'world' → 'there'
    insert(11, '!'),
  ]); // text1 = 'hello there!'
  assert.equal(r1.text.text, 'hello there!');
  const r2 = applyOps(r1.text, [
    copy(0, 6), // 'hello '
    copy(6, 11), // 'there'
    insert(11, '~'),
    copy(6, 12), // 'there!'
  ]); // text2 = 'hello there~there!'
  assert.equal(r2.text.text, 'hello there~there!');
  const m = composeMappings(r1.mapping, r2.mapping);

  assert.equal(m.inputLength, 11);
  assert.equal(m.outputLength, 18);
  assert.deepEqual(querySource(m, 6, 11), [{ start: 6, end: 11 }]);
  assert.deepEqual(querySource(m, 11, 12), [{ start: 11, end: 11 }], "'~' 锚定在 text0 末尾");
  assert.deepEqual(querySource(m, 17, 18), [{ start: 11, end: 11 }], "第二轮复制的 '!' 仍是插入锚点");
  assert.deepEqual(queryOutput(m, 6, 11), [
    { start: 6, end: 11 },
    { start: 12, end: 17 },
  ]);
});

// ---------- 随机模糊：逐字符标签模型交叉核对 ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomText(rnd: () => number, length: number): string {
  let out = '';
  for (let i = 0; i < length; i++) out += String.fromCharCode(97 + Math.floor(rnd() * 26));
  return out;
}

function randomOps(rnd: () => number, inputLen: number): Op[] {
  const ops: Op[] = [];
  let pos = 0;
  const maybeInsert = () => {
    if (rnd() < 0.25) ops.push(insert(pos, randomText(rnd, 1 + Math.floor(rnd() * 3))));
  };
  while (pos < inputLen) {
    maybeInsert();
    const remain = inputLen - pos;
    const r = rnd();
    if (r < 0.4) {
      const len = 1 + Math.floor(rnd() * Math.min(4, remain));
      ops.push(copy(pos, pos + len));
      pos += len;
    } else if (r < 0.65) {
      const len = 1 + Math.floor(rnd() * Math.min(4, remain));
      ops.push(replace(pos, pos + len, randomText(rnd, Math.floor(rnd() * 5)))); // 可能为空 = 删除
      pos += len;
    } else if (r < 0.8) {
      pos += 1 + Math.floor(rnd() * Math.min(3, remain)); // 删除，制造来源空隙
    } else {
      const a = Math.floor(rnd() * inputLen);
      const b = a + 1 + Math.floor(rnd() * (inputLen - a));
      ops.push(copy(a, b)); // 复制任意区间（可能重复 → 反向多值）
    }
  }
  maybeInsert();
  return ops;
}

function stringApply(source: string, ops: Op[]): string {
  let out = '';
  for (const op of ops) {
    out += op.type === 'copy' ? source.slice(op.start, op.end) : op.text;
  }
  return out;
}

function verifyRoundTrip(
  composed: ReturnType<typeof composeMappings>,
  finalText: TaggedText,
  sourceLen: number,
  rnd: () => number,
  ctx: string,
): void {
  for (let i = 0; i < finalText.length; i++) {
    assert.deepEqual(querySource(composed, i, i + 1), finalText.chars[i].frags, `${ctx}: 字符 ${i}`);
  }
  assert.deepEqual(
    querySource(composed, 0, finalText.length),
    expectedForward(finalText, 0, finalText.length),
    `${ctx}: 全文正向`,
  );
  for (let k = 0; k < 10; k++) {
    const q0 = Math.floor(rnd() * (finalText.length + 1));
    const q1 = q0 + Math.floor(rnd() * (finalText.length - q0 + 1));
    assert.deepEqual(querySource(composed, q0, q1), expectedForward(finalText, q0, q1), `${ctx}: 正向 [${q0}, ${q1})`);
  }
  for (let s = 0; s < sourceLen; s++) {
    assert.deepEqual(queryOutput(composed, s, s + 1), expectedReverse(finalText, s, s + 1), `${ctx}: 反向字符 ${s}`);
  }
  for (let k = 0; k < 10; k++) {
    const s0 = Math.floor(rnd() * (sourceLen + 1));
    const s1 = s0 + Math.floor(rnd() * (sourceLen - s0 + 1));
    assert.deepEqual(queryOutput(composed, s0, s1), expectedReverse(finalText, s0, s1), `${ctx}: 反向 [${s0}, ${s1})`);
  }
}

for (const seed of [20261006, 42, 987654321]) {
  test(`随机模糊：两轮转换组合 vs 逐字符来源标签模型（种子 ${seed}，500 轮）`, () => {
    const rnd = mulberry32(seed);
    for (let iter = 0; iter < 500; iter++) {
      const source = randomText(rnd, Math.floor(rnd() * 13));
      const ops1 = randomOps(rnd, source.length);
      const t0 = TaggedText.source(source);
      const r1 = applyOps(t0, ops1);
      assert.equal(r1.text.text, stringApply(source, ops1), `迭代 ${iter}: 第一轮文本`);

      const ops2 = randomOps(rnd, r1.text.length);
      const r2 = applyOps(r1.text, ops2);
      assert.equal(r2.text.text, stringApply(r1.text.text, ops2), `迭代 ${iter}: 第二轮文本`);

      const composed = composeMappings(r1.mapping, r2.mapping);
      assert.equal(composed.inputLength, source.length);
      assert.equal(composed.outputLength, r2.text.length);
      verifyRoundTrip(composed, r2.text, source.length, rnd, `迭代 ${iter} (源=${JSON.stringify(source)})`);
    }
  });
}
