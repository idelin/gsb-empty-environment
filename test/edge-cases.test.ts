import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateGraphQL } from '../src/index.ts';
import { policyFor, tenantSchema } from './fixtures.ts';

test('敏感参数即使经变量传入也被拦截', () => {
  const query = `
    query ViaVar($tenant: ID!) {
      users(first: 1, tenantId: $tenant) { id }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(
    result.violations.some((item) => item.code === 'SENSITIVE_ARG_OVERRIDE'),
  );
});

test('内联片段条件分支中的受限字段仍被拦截', () => {
  const query = `
    query InlineCond($flag: Boolean!) {
      me {
        ... on User @include(if: $flag) {
          tenantToken
        }
      }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('admin'), query);
  const violation = result.violations.find((item) => item.code === 'FIELD_FORBIDDEN');
  assert.ok(violation);
  assert.equal(violation?.path, 'Query.me.tenantToken');
});

test('非布尔字面量用于 @include 报类型不匹配', () => {
  const query = `{ me { name @include(if: "yes") } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(
    result.violations.some((item) => item.code === 'VARIABLE_TYPE_MISMATCH'),
  );
});

test('列表变量传入标量参数报结构不匹配', () => {
  const query = `
    query ListScalar($ids: [ID!]!) {
      user(id: $ids) { id }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(
    result.violations.some((item) => item.code === 'VARIABLE_TYPE_MISMATCH'),
  );
});

test('非法列表分页参数报 INVALID_LIST_ARGUMENT', () => {
  const query = `{ users(first: "many") { id } }`;
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer', { maxAliasMultiplier: 500 }),
    query,
  );
  assert.ok(
    result.violations.some((item) => item.code === 'INVALID_LIST_ARGUMENT'),
  );
});

test('mutation 根类型解析与字段授权联动', () => {
  const query = `mutation { updateUser(id: "1", scope: { region: "cn" }) { id } }`;
  const viewerResult = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(
    viewerResult.violations.some((item) => item.code === 'FIELD_FORBIDDEN'),
  );
  const adminResult = validateGraphQL(tenantSchema, policyFor('admin'), query);
  assert.deepEqual(
    adminResult.violations.map((item) => item.code),
    [],
  );
});

test('限额违规携带确定性复杂度上界快照', () => {
  const query = `{ users(first: 40) { id } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  const aliasLimit = result.violations.find(
    (item) => item.code === 'LIMIT_ALIAS_EXCEEDED',
  );
  assert.ok(aliasLimit?.bounds);
  // users 列表字段按基数计 40 次 + 40 个 id 字段解析
  assert.equal(aliasLimit?.bounds?.aliasMultiplier, 80);
  assert.equal(aliasLimit?.bounds?.complexityUpperBound, 5 * 40 + 40);
});
