import assert from 'node:assert/strict';
import {describe, test} from 'node:test';
import {Level} from '../src/levels.ts';
import {PolicyEngine} from '../src/policy.ts';
import type {Subject} from '../src/policy.ts';

const schema = {
  id: Level.PUBLIC,
  name: Level.INTERNAL,
  email: Level.CONFIDENTIAL,
  bankAccount: Level.SECRET,
} as const;

const analyst: Subject = {
  id: 'u-1001',
  name: 'analyst',
  readClearance: Level.CONFIDENTIAL,
  exportClearance: Level.INTERNAL,
};

describe('PolicyEngine 字段可见范围', () => {
  const engine = new PolicyEngine(schema);

  test('按主体读取级别裁剪可见字段', () => {
    assert.deepEqual(engine.visibleFieldsOf(analyst), ['id', 'name', 'email']);
    assert.equal(engine.canSeeField(analyst, 'bankAccount'), false);
    assert.equal(engine.canSeeField(analyst, 'email'), true);
  });

  test('未知字段一律不可见', () => {
    assert.equal(engine.canSeeField(analyst, 'not-a-field'), false);
  });
});

describe('PolicyEngine 越级读取判定', () => {
  const engine = new PolicyEngine(schema);

  test('合法读取：全部字段在级别内则放行', () => {
    const decision = engine.evaluateRead(analyst, ['id', 'name', 'email']);
    assert.equal(decision.allowed, true);
    assert.deepEqual(decision.visibleFields, ['id', 'name', 'email']);
    assert.deepEqual(decision.violatingFields, []);
  });

  test('越级读取：含 SECRET 字段时整次请求拒绝', () => {
    const decision = engine.evaluateRead(analyst, ['name', 'bankAccount']);
    assert.equal(decision.allowed, false);
    assert.deepEqual(decision.visibleFields, []);
    assert.equal(decision.violatingFields[0].field, 'bankAccount');
    assert.equal(decision.violatingFields[0].level, Level.SECRET);
    assert.match(decision.reason, /Cross-level READ denied/);
  });

  test('请求字段去重后判定', () => {
    const decision = engine.evaluateRead(analyst, ['id', 'id', 'name']);
    assert.equal(decision.allowed, true);
    assert.deepEqual(decision.visibleFields, ['id', 'name']);
  });
});

describe('PolicyEngine 批量导出组合判定', () => {
  const engine = new PolicyEngine(schema);

  test('合法导出：组合字段不超过导出级别', () => {
    const decision = engine.evaluateExport(analyst, ['id', 'name']);
    assert.equal(decision.allowed, true);
    assert.deepEqual(decision.visibleFields, ['id', 'name']);
  });

  test('拒绝导出组合：可读但不可导出的高敏字段混入即整批拒绝', () => {
    const decision = engine.evaluateExport(analyst, ['id', 'name', 'email']);
    assert.equal(decision.allowed, false);
    assert.deepEqual(decision.visibleFields, []);
    assert.deepEqual(
      decision.violatingFields.map((entry) => entry.field),
      ['email'],
    );
    assert.match(decision.reason, /Bulk EXPORT denied/);
  });

  test('未知字段进入导出组合同样拒绝', () => {
    const decision = engine.evaluateExport(analyst, ['id', 'ghost']);
    assert.equal(decision.allowed, false);
    assert.equal(decision.violatingFields[0].field, 'ghost');
  });
});
