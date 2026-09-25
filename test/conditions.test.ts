import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  evaluateCondition,
  validateCondition,
  type AttrValue,
  type Condition,
} from '../src/conditions.ts';

function recorder(values: Record<string, AttrValue | undefined>) {
  const reads: string[] = [];
  const read = (ref: string): AttrValue | undefined => {
    reads.push(ref);
    return values[ref];
  };
  return { read, reads };
}

test('有限集合成员判定', () => {
  const cond: Condition = { op: 'in', attr: 'subject.role', values: ['admin', 'editor'] };
  assert.equal(evaluateCondition(cond, () => 'admin'), true);
  assert.equal(evaluateCondition(cond, () => 'guest'), false);
  assert.equal(evaluateCondition(cond, () => undefined), false);
});

test('整数条件比较', () => {
  const ge9: Condition = { op: 'ge', attr: 'context.hour', value: 9 };
  assert.equal(evaluateCondition(ge9, () => 9), true);
  assert.equal(evaluateCondition(ge9, () => 8), false);
  assert.equal(evaluateCondition(ge9, () => 9.5), false, '非整数属性值不满足整数条件');
  assert.equal(evaluateCondition(ge9, () => '9'), false, '字符串不冒充整数');
  assert.equal(evaluateCondition(ge9, () => undefined), false, '缺失属性不满足整数条件');
});

test('and 短路：被跳过的属性不会被读取', () => {
  const cond: Condition = {
    op: 'and',
    all: [
      { op: 'in', attr: 'subject.role', values: ['admin'] },
      { op: 'ge', attr: 'context.hour', value: 9 },
    ],
  };
  const { read, reads } = recorder({ 'subject.role': 'guest', 'context.hour': 10 });
  assert.equal(evaluateCondition(cond, read), false);
  assert.deepEqual(reads, ['subject.role'], 'context.hour 不应被读取');
});

test('or 短路：首个分支命中后不再读取后续属性', () => {
  const cond: Condition = {
    op: 'or',
    any: [
      { op: 'in', attr: 'subject.role', values: ['admin'] },
      { op: 'in', attr: 'subject.department', values: ['eng'] },
    ],
  };
  const { read, reads } = recorder({ 'subject.role': 'admin', 'subject.department': 'eng' });
  assert.equal(evaluateCondition(cond, read), true);
  assert.deepEqual(reads, ['subject.role']);
});

test('not 运算', () => {
  const cond: Condition = { op: 'not', cond: { op: 'in', attr: 'subject.role', values: ['guest'] } };
  assert.equal(evaluateCondition(cond, () => 'admin'), true);
  assert.equal(evaluateCondition(cond, () => 'guest'), false);
});

test('条件校验：拒绝有限集合/整数/布尔运算之外的内容', () => {
  assert.throws(
    () => validateCondition({ op: 'ge', attr: 'context.hour', value: 9.5 }),
    /整数/,
    '整数条件的操作数必须是整数',
  );
  assert.throws(
    () => validateCondition({ op: 'in', attr: 'subject.role', values: [] }),
    /非空/,
    '集合必须非空',
  );
  assert.throws(
    () => validateCondition({ op: 'in', attr: 'role', values: ['a'] }),
    /subject\.<name> 或 context\.<name>/,
    '属性引用必须显式归属主体或上下文',
  );
  assert.throws(
    () => validateCondition({ op: 'in', attr: 'subject.score', values: [1.5] }),
    /字符串、布尔或整数/,
    '集合元素不允许非整数数值',
  );
});
