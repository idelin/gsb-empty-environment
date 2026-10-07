import { test, assert } from './helpers.mjs';
import { replaceAll, applyForward, applyInverse } from '../dist/index.js';

// 朴素拼接模型：按编辑切片重组文本，作为位置映射的独立参照。
function naiveApply(text, edits) {
  let out = '';
  let cursor = 0;
  for (const e of edits) {
    out += text.slice(cursor, e.oldStart) + e.newText;
    cursor = e.oldEnd;
  }
  return out + text.slice(cursor);
}

test('edits reconstruct new text under the naive concatenation model', () => {
  const cases = [
    ['hello world', /o/g, '0'],
    ['aab', /a*/g, '-'],
    ['abc', /(?:)/g, '|'],
    ['aaaa', /aa/g, 'b'],
    ['aXbXc', /X/g, '$`~'],
    ['abc', /b/, ''],
    ['aa', /a/g, 'bb'],
    ['one two', 'o', '0'],
  ];
  for (const [text, re, tpl] of cases) {
    const r = replaceAll(text, re, tpl);
    assert.equal(naiveApply(text, r.edits), r.newText, JSON.stringify([text, re.source, tpl]));
    assert.equal(applyForward(text, r.edits), r.newText);
    assert.equal(applyInverse(r.newText, r.edits), text);
  }
});

test('kept ranges before/after matches map to identical text', () => {
  const text = 'one two three';
  const r = replaceAll(text, /two/, '2');
  // 匹配前的保留区 "one "
  assert.equal(r.newText.slice(r.mapPosition(0), r.mapPosition(4)), text.slice(0, 4));
  // 匹配后的保留区 " three"
  assert.equal(r.newText.slice(r.mapPosition(7), r.mapPosition(13)), text.slice(7, 13));
});

test('empty replacement collapses the mapped range to a point', () => {
  const r = replaceAll('abc', /b/, '');
  assert.equal(r.newText, 'ac');
  assert.equal(r.mapPosition(0), 0);
  assert.equal(r.mapPosition(1), 1);
  assert.equal(r.mapPosition(2), 1);
  assert.equal(r.mapPosition(3), 2);
  assert.deepEqual(r.mapRange(1, 2), [1, 1]);
});

test('adjacent matches share a mapped boundary', () => {
  const r = replaceAll('aa', /a/g, 'bb');
  assert.equal(r.mapPosition(0), 0);
  assert.equal(r.mapPosition(1), 2);
  assert.equal(r.mapPosition(2), 4);
  assert.deepEqual(r.mapRange(0, 2), [0, 4]);
});

test('zero-width insertion: assoc brackets the inserted text', () => {
  const r = replaceAll('ab', /(?:)/g, '-');
  assert.equal(r.newText, '-a-b-');
  assert.equal(r.mapPosition(1, 'start'), 2);
  assert.equal(r.mapPosition(1, 'end'), 3);
  assert.equal(r.newText.slice(2, 3), '-');
  // 边界 0 与 2（文本两端）
  assert.equal(r.mapPosition(0, 'start'), 0);
  assert.equal(r.mapPosition(0, 'end'), 1);
  assert.equal(r.mapPosition(2, 'start'), 4);
  assert.equal(r.mapPosition(2, 'end'), 5);
});

test('positions inside a replaced region follow assoc', () => {
  const r = replaceAll('abcd', /bc/, 'XYZ');
  assert.equal(r.newText, 'aXYZd');
  assert.equal(r.mapPosition(2, 'start'), 1);
  assert.equal(r.mapPosition(2, 'end'), 4);
  assert.deepEqual(r.mapRange(1, 3), [1, 4]);
  assert.deepEqual(r.mapRange(0, 4), [0, 5]);
});

test('mapping stays consistent with naive model across random-ish cases', () => {
  const cases = [
    ['aXbXcXd', /X/g, 'YY'],
    ['aaaa', /aa/g, 'b'],
    ['xyz', /(?:)/g, '..'],
    ['abcabc', /abc/g, ''],
  ];
  for (const [text, re, tpl] of cases) {
    const r = replaceAll(text, re, tpl);
    // 每个保留间隙映射后切片相等
    let cursor = 0;
    for (const e of r.edits) {
      const a = cursor;
      const b = e.oldStart;
      // 提取保留文本：左边缘取 'end'（跳过该边界上的零宽插入），右边缘取 'start'
      assert.equal(
        r.newText.slice(r.mapPosition(a, 'end'), r.mapPosition(b, 'start')),
        text.slice(a, b),
        `gap [${a},${b}) of ${JSON.stringify([text, re.source, tpl])}`,
      );
      cursor = e.oldEnd;
    }
    assert.equal(
      r.newText.slice(r.mapPosition(cursor, 'end'), r.mapPosition(text.length, 'start')),
      text.slice(cursor),
    );
    // 映射单调不减
    let prev = 0;
    for (let p = 0; p <= text.length; p++) {
      const mapped = r.mapPosition(p, 'start');
      assert.ok(mapped >= prev, `monotonic at ${p}`);
      prev = mapped;
    }
  }
});
