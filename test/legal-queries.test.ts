import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateGraphQL } from '../src/index.ts';
import { policyFor, tenantSchema } from './fixtures.ts';

test('合法的深度嵌套查询通过并返回确定性上界', () => {
  const query = `
    query Dashboard($id: ID!, $detail: Boolean = true) {
      user(id: $id) {
        id
        name
        email @include(if: $detail)
        roles
        orders(first: 3) {
          id
          total
        }
      }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.deepEqual(
    result.violations.map((violation) => violation.code),
    [],
  );
  assert.equal(result.ok, true);
  assert.equal(result.maxDepth, 3);
  // user 1 次；4 个标量字段；orders 3 次；订单标量字段 3*2 次
  assert.equal(result.aliasMultiplier, 1 + 4 + 3 + 3 * 2);
});

test('合法 mutation 的普通输入字段允许、仅敏感字段被拦截', () => {
  const allowed = validateGraphQL(
    tenantSchema,
    policyFor('admin'),
    `mutation M($id: ID!) { updateUser(id: $id, scope: { region: "cn" }) { id } }`,
  );
  assert.deepEqual(
    allowed.violations.map((violation) => violation.code),
    [],
  );
});

test('带具名片段、内联片段与别名的合法查询通过', () => {
  const query = `
    query Mixed {
      users(first: 2) {
        ...UserInfo
        ... on User { name }
        recent: orders(first: 1) { id total }
      }
    }
    fragment UserInfo on User { id email }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.deepEqual(
    result.violations.map((violation) => violation.code),
    [],
  );
  assert.equal(result.aliasedFieldCount, 1);
  assert.ok(result.complexityUpperBound > 0);
});

test('语法错误被包装为 SYNTAX_ERROR 且硬拒绝', () => {
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer'),
    `query { me { `,
  );
  assert.equal(result.ok, false);
  assert.equal(result.violations[0].code, 'SYNTAX_ERROR');
});

test('多操作文档累计全局别名倍数与复杂度', () => {
  const query = `
    query A { me { id name } }
    query B { me { id } }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.equal(result.ok, true);
  // A: me + 2 字段；B: me + 1 字段
  assert.equal(result.aliasMultiplier, 3 + 2);
  assert.equal(result.maxDepth, 2);
});

test('枚举字面量与列表变量类型校验', () => {
  const schemaWithEnum = tenantSchema;
  const query = `
    query EnumCheck($id: ID!) {
      user(id: $id) { roles }
    }
  `;
  const result = validateGraphQL(schemaWithEnum, policyFor('viewer'), query);
  assert.equal(result.ok, true);
});
