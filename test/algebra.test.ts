import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  audit, format, limit, mask, orderedSteps, mergeObligations,
  type Obligation,
} from '../src/index.ts';

test('限额合并取更严格值：上限取最小，下限取最大', () => {
  const result = mergeObligations([
    limit('maxRows', 'max', 1000, 'ruleA'),
    limit('maxRows', 'max', 100, 'ruleB'),
    limit('minRows', 'min', 10, 'ruleA'),
    limit('minRows', 'min', 50, 'ruleC'),
  ]);
  assert.equal(result.status, 'ok');
  if (result.status !== 'ok') return;
  assert.equal(result.solution.bounds['maxRows']!.hi, 100);
  assert.equal(result.solution.bounds['minRows']!.lo, 50);
  // 证据记录所有贡献者与决定者
  assert.deepEqual(
    result.evidence.limits['maxRows']!.contributors.sort(),
    ['ruleA', 'ruleB'],
  );
});

test('遮蔽字段合并取并集', () => {
  const result = mergeObligations([
    mask(['ssn', 'salary'], 'ruleA'),
    mask(['salary', 'address'], 'ruleB'),
  ]);
  assert.equal(result.status, 'ok');
  if (result.status !== 'ok') return;
  assert.deepEqual(result.solution.maskedFields, ['address', 'salary', 'ssn']);
  assert.deepEqual(result.evidence.maskedFields['salary']!.sort(), ['ruleA', 'ruleB']);
  assert.deepEqual(result.evidence.maskedFields['ssn'], ['ruleA']);
});

test('审计标签合并顺序无关：满足交换律', () => {
  const a: Obligation[] = [audit(['finance'], 'ruleA'), mask(['ssn'], 'ruleA')];
  const b: Obligation[] = [audit(['pii'], 'ruleB'), mask(['dob'], 'ruleB')];
  const ab = mergeObligations([...a, ...b]);
  const ba = mergeObligations([...b, ...a]);
  assert.equal(ab.status, 'ok');
  assert.equal(ba.status, 'ok');
  if (ab.status !== 'ok' || ba.status !== 'ok') return;
  assert.deepEqual(ab.solution.auditTags, ba.solution.auditTags);
  assert.deepEqual(ab.solution.maskedFields, ba.solution.maskedFields);
  assert.deepEqual(ab.solution.auditTags, ['finance', 'pii']);
});

test('顺序敏感义务显式保序：不满足交换律', () => {
  const a = orderedSteps(['tokenize', 'encrypt'], 'ruleA');
  const b = orderedSteps(['audit-log'], 'ruleB');
  const ab = mergeObligations([a, b]);
  const ba = mergeObligations([b, a]);
  assert.equal(ab.status, 'ok');
  assert.equal(ba.status, 'ok');
  if (ab.status !== 'ok' || ba.status !== 'ok') return;
  assert.deepEqual(ab.solution.steps, ['tokenize', 'encrypt', 'audit-log']);
  assert.deepEqual(ba.solution.steps, ['audit-log', 'tokenize', 'encrypt']);
  // 保序：位置与来源被显式记录
  assert.deepEqual(
    ab.evidence.steps.map((s) => [s.position, s.step, s.source]),
    [[0, 'tokenize', 'ruleA'], [1, 'encrypt', 'ruleA'], [2, 'audit-log', 'ruleB']],
  );
});

test('两个互斥输出格式要求导致明确失败，不静默择一', () => {
  const result = mergeObligations([
    format('json', 'ruleA'),
    format('csv', 'ruleB'),
  ]);
  assert.equal(result.status, 'conflict');
  if (result.status !== 'conflict') return;
  assert.match(result.message, /互斥输出格式冲突/);
  assert.equal(result.conflicting.length, 2);
  // 冲突双方都被保留，未丢弃任何一方
  assert.deepEqual(
    result.conflicting.map((o) => o.kind === 'format' ? o.format : '').sort(),
    ['csv', 'json'],
  );
});

test('相同输出格式可正常合并', () => {
  const result = mergeObligations([
    format('json', 'ruleA'),
    format('json', 'ruleB'),
  ]);
  assert.equal(result.status, 'ok');
  if (result.status !== 'ok') return;
  assert.equal(result.solution.format, 'json');
  assert.deepEqual(result.evidence.format!.sources.sort(), ['ruleA', 'ruleB']);
});
