import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateGraphQL } from '../src/index.ts';
import { policyFor, tenantSchema } from './fixtures.ts';

test('直接自引用片段被检出并给出展开链', () => {
  const query = `
    { me { ...A } }
    fragment A on User { id ...A }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.equal(result.ok, false);
  const violation = result.violations.find((item) => item.code === 'FRAGMENT_CYCLE');
  assert.ok(violation);
  assert.deepEqual(violation?.fragmentChain, ['A', 'A']);
});

test('间接循环片段 A -> B -> C -> A 被检出', () => {
  const query = `
    { me { ...A } }
    fragment A on User { id ...B }
    fragment B on User { name ...C }
    fragment C on User { email ...A }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.equal(result.ok, false);
  const violation = result.violations.find((item) => item.code === 'FRAGMENT_CYCLE');
  assert.ok(violation);
  assert.deepEqual(violation?.fragmentChain, ['A', 'B', 'C', 'A']);
});

test('具名片段与内联片段嵌套但无环时正常统计深度', () => {
  const query = `
    query WithFragments {
      users(first: 2) {
        ...UserCore
        ... on User {
          orders(first: 2) { ...OrderCore }
        }
      }
    }
    fragment UserCore on User { id name ...EmailFrag }
    fragment EmailFrag on User { email }
    fragment OrderCore on Order { id total }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.deepEqual(
    result.violations.map((violation) => violation.code),
    [],
  );
  assert.equal(result.maxDepth, 3);
});

test('DAG 片段复用按使用点倍数累计且不会指数爆炸', () => {
  const query = `
    query Reuse {
      me {
        ...Shared
        ...Shared
      }
    }
    fragment Shared on User { id name }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.equal(result.ok, true);
  // me(1) + 两次 Shared 展开，每次 2 个字段
  assert.equal(result.aliasMultiplier, 1 + 2 * 2);
  assert.equal(result.complexityUpperBound, 2 + 2 * 2);
});

test('未定义片段引用报 UNKNOWN_FRAGMENT', () => {
  const query = `{ me { ...Missing } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.equal(result.ok, false);
  assert.ok(result.violations.some((violation) => violation.code === 'UNKNOWN_FRAGMENT'));
});

test('重复片段定义报 DUPLICATE_FRAGMENT', () => {
  const query = `
    { me { ...A } }
    fragment A on User { id }
    fragment A on User { name }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(result.violations.some((violation) => violation.code === 'DUPLICATE_FRAGMENT'));
});
