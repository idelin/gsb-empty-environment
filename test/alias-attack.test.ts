import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateGraphQL } from '../src/index.ts';
import { policyFor, tenantSchema } from './fixtures.ts';

test('同层数百个别名放大攻击触发别名倍数与复杂度硬拒绝', () => {
  const aliases = Array.from(
    { length: 60 },
    (_, index) => `attempt${index}: loginAttempt(email: "u${index}@x.com", password: "p${index}") { success }`,
  ).join('\n');
  const query = `query Bruteforce { ${aliases} }`;

  const result = validateGraphQL(tenantSchema, policyFor('admin'), query);
  assert.equal(result.ok, false);
  assert.equal(result.aliasMultiplier, 120);
  assert.equal(result.aliasedFieldCount, 60);
  assert.ok(result.violations.some((violation) => violation.code === 'LIMIT_ALIAS_EXCEEDED'));
  assert.ok(
    result.violations.some((violation) => violation.code === 'LIMIT_COMPLEXITY_EXCEEDED'),
  );
  const limitViolation = result.violations.find(
    (violation) => violation.code === 'LIMIT_ALIAS_EXCEEDED',
  );
  assert.equal(limitViolation?.bounds?.aliasMultiplier, 120);
  assert.equal(limitViolation?.bounds?.complexityUpperBound, 660);
});

test('列表 first/limit 乘法因子计入确定性复杂度上界', () => {
  const query = `
    query ListAttack {
      users(first: 10) {
        id
        orders(first: 5) { id total }
      }
    }
  `;
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer', { maxAliasMultiplier: 500 }),
    query,
  );
  // users 权重 5 * 10；内部 orders 每个用户一次，权重 1 * 5，共 10 * 5
  // users(id) 每元素 1 * 10；orders 内 id/total 各 1，共 10 * 5 * 2
  const expected = 5 * 10 + 10 * (1 + 5 + 5 * 2);
  assert.equal(result.complexityUpperBound, expected);
  // 完全展开后的字段/解析器调用总上界：users(1) + User.id(10) +
  // Order 节点(10*5) + Order 字段(10*5*2) = 170
  assert.equal(result.aliasMultiplier, 170);
  assert.equal(result.ok, true);
});

test('变量分页参数按 maxListSize 取最坏情况上界', () => {
  const query = `
    query Paged($size: Int) {
      users(first: $size) { id }
    }
  `;
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer', { maxAliasMultiplier: 500 }),
    query,
  );
  assert.equal(result.ok, true);
  assert.equal(result.complexityUpperBound, 5 * 50 + 50);
});

test('超大显式分页值被收敛到 maxListSize 而非无限放大', () => {
  const query = `{ users(first: 999999) { id } }`;
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer', { maxAliasMultiplier: 500 }),
    query,
  );
  assert.equal(result.complexityUpperBound, 5 * 50 + 50);
});

test('深度超限在展开片段后按真实深度判定', () => {
  const query = `
    query Deep {
      me { orders { id } }
    }
  `;
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer', { maxDepth: 2 }),
    query,
  );
  assert.equal(result.ok, false);
  assert.equal(result.maxDepth, 3);
  assert.ok(result.violations.some((violation) => violation.code === 'LIMIT_DEPTH_EXCEEDED'));
});
