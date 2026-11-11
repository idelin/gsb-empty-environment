import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runBacktracking, StepCapExceededError } from '../index.ts';

test('回溯解释器处理字面量与连接', () => {
  assert.equal(runBacktracking('abc', 'abc').matched, true);
  assert.equal(runBacktracking('abc', 'abd').matched, false);
  assert.equal(runBacktracking('abc', 'abcd').matched, false);
});

test('整串语义下部分匹配不算成功', () => {
  assert.equal(runBacktracking('ab', 'abc').matched, false);
  assert.equal(runBacktracking('ab', 'xab').matched, false);
});

test('星号贪婪匹配与零次重复', () => {
  assert.equal(runBacktracking('a*', '').matched, true);
  assert.equal(runBacktracking('a*', 'aaaa').matched, true);
  assert.equal(runBacktracking('a*b', 'aaab').matched, true);
  assert.equal(runBacktracking('a*b', 'aaa').matched, false);
});

test('并集按顺序回溯并最终命中后续分支', () => {
  assert.equal(runBacktracking('ab|a', 'a').matched, true);
  assert.equal(runBacktracking('ab|a', 'ab').matched, true);
  assert.equal(runBacktracking('ab|a', 'ac').matched, false);
});

test('锚点被记录但不改变整串判定结果', () => {
  const anchored = runBacktracking('^a*b$', 'aaab');
  const plain = runBacktracking('a*b', 'aaab');
  assert.equal(anchored.matched, true);
  assert.equal(plain.matched, true);
  assert.ok(anchored.trace.some((event) => event.kind === 'anchor'));
});

test('步数随输入增长且每次进入节点都计数', () => {
  const small = runBacktracking('a*', 'aa');
  const large = runBacktracking('a*', 'aaaaaa');
  assert.ok(large.steps > small.steps);
});

test('命中步数上限时 capped 为 true 且不抛出', () => {
  // (a|aa)* 的回溯步数按 Fibonacci 增长，30 个 a 配合很小的上限必然被截断。
  const result = runBacktracking('(a|aa)*$', 'a'.repeat(30) + '~', {
    stepCap: 1000,
  });
  assert.equal(result.capped, true);
  assert.equal(result.matched, false);
});

test('回溯轨迹受 maxTraceEvents 限制', () => {
  const result = runBacktracking('(a|aa)*$', 'aaaaaaaa', {
    stepCap: 5000,
    maxTraceEvents: 10,
  });
  assert.ok(result.trace.length <= 10);
});

test('StepCapExceededError 携带上限值', () => {
  const error = new StepCapExceededError(42);
  assert.equal(error.cap, 42);
  assert.equal(error.name, 'StepCapExceededError');
});

test('指数回溯见证在解释器上呈现超线性步数', () => {
  const r1 = runBacktracking('(a|aa)*$', 'a'.repeat(10) + '~');
  const r2 = runBacktracking('(a|aa)*$', 'a'.repeat(16) + '~', { stepCap: 5_000_000 });
  assert.equal(r1.matched, false);
  assert.equal(r2.matched, false);
  // 输入长度仅增加 6，步数应急剧放大。
  assert.ok(r2.steps > r1.steps * 8);
});
