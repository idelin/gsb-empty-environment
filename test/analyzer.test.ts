import { test } from 'node:test';
import assert from 'node:assert/strict';
import { analyzePattern } from '../index.ts';

test('确认经典指数回溯模式并给出完整见证', () => {
  const report = analyzePattern('(a|aa)*$', { pumpCounts: [0, 4, 8, 12] });
  assert.equal(report.status, 'confirmed');
  assert.ok(report.witness !== null);
  assert.equal(typeof report.witness!.prefix, 'string');
  assert.ok(report.witness!.pump.length > 0);
  assert.ok(report.witness!.failingSuffix.endsWith(report.witness!.deadChar));
  assert.ok(report.growth.length >= 2);
});

test('嵌套星号歧义被确认且最终撞上限或高比值', () => {
  const report = analyzePattern('^((ab)*)*$', { pumpCounts: [0, 2, 4, 6, 8] });
  assert.equal(report.status, 'confirmed');
  const last = report.growth[report.growth.length - 1];
  const strongRatio = report.growth.some(
    (sample) => sample.ratioToPrevious !== null && sample.ratioToPrevious >= 1.6,
  );
  assert.ok(last.capped || strongRatio);
});

test('唯一解析模式返回 no-evidence 且不带见证', () => {
  for (const source of ['a*$', '(a|b)*$', '^a*b$', '(foo|foot)*$']) {
    const report = analyzePattern(source);
    assert.equal(report.status, 'no-evidence', source);
    assert.equal(report.witness, null);
    assert.equal(report.growth.length, 0);
  }
});

test('no-evidence 报告仍携带不免责声明', () => {
  const report = analyzePattern('a*$');
  assert.match(report.disclaimer, /不等于正则安全|未找到证据/);
  assert.match(report.reason, /未证实|不能据此宣称/);
});

test('所有状态的报告都包含统一免责声明', () => {
  for (const source of ['(a|aa)*$', 'a*$']) {
    const report = analyzePattern(source, { pumpCounts: [0, 2, 4] });
    assert.match(report.disclaimer, /no-evidence|未找到证据|不能据此/);
    assert.ok(report.disclaimer.length > 20);
  }
});

test('增长样本记录输入、步数、撞顶标记与比值', () => {
  const report = analyzePattern('a(b|b)*c', { pumpCounts: [0, 2, 4] });
  assert.equal(report.status, 'confirmed');
  for (const sample of report.growth) {
    assert.equal(typeof sample.input, 'string');
    assert.ok(sample.steps > 0);
    assert.equal(typeof sample.capped, 'boolean');
  }
  assert.equal(report.growth[0].ratioToPrevious, null);
  for (const sample of report.growth.slice(1)) {
    assert.equal(typeof sample.ratioToPrevious, 'number');
  }
});

test('泵序列撞顶后提前结束采样', () => {
  const report = analyzePattern('((a|a)*)$', {
    stepCap: 1000,
    pumpCounts: [0, 2, 4, 6, 8, 10],
  });
  assert.equal(report.status, 'confirmed');
  assert.ok(report.growth[report.growth.length - 1].capped);
  assert.ok(report.growth.length < 6);
});

test('语法错误向上抛出自定义错误而不是伪装成分析结论', () => {
  assert.throws(() => analyzePattern('a+'));
});

test('confirmed 报告的见证输入确实匹配失败', () => {
  const report = analyzePattern('(a|aa)*$', { pumpCounts: [0, 2] });
  const sample = report.growth[0];
  assert.ok(sample.input.endsWith(report.witness!.deadChar));
});
