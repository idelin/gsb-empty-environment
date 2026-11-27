import assert from 'node:assert/strict';
import { test } from 'node:test';
import { validateGraphQL } from '../src/index.ts';
import { policyFor, tenantSchema } from './fixtures.ts';

test('@include/@skip 条件分支中的受限字段仍被最坏情况授权拦截', () => {
  const query = `
    query Conditional($flag: Boolean!) {
      me {
        name
        tenantToken @include(if: $flag)
        email @skip(if: $flag)
      }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('admin'), query);
  assert.equal(result.ok, false);
  const forbidden = result.violations.filter(
    (violation) => violation.code === 'FIELD_FORBIDDEN',
  );
  assert.equal(forbidden.length, 1);
  assert.equal(forbidden[0].path, 'Query.me.tenantToken');
});

test('片段深处的受限字段输出规范路径与片段展开链', () => {
  const query = `
    query Hidden {
      users(first: 1) {
        ...A
      }
    }
    fragment A on User { orders(first: 1) { ...B } }
    fragment B on Order { secret }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  const violation = result.violations.find((item) => item.code === 'FIELD_FORBIDDEN');
  assert.ok(violation);
  assert.equal(violation?.path, 'Query.users.orders.secret');
  assert.deepEqual(violation?.fragmentChain, ['A', 'B']);
});

test('内省字段默认被阻断', () => {
  const query = `{ __schema { types { name } } __type(name: "User") { name } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.equal(result.ok, false);
  assert.equal(
    result.violations.filter((violation) => violation.code === 'INTROSPECTION_BLOCKED').length,
    2,
  );
});

test('关闭内省阻断后 __schema 可通过且 __typename 始终允许', () => {
  const query = `{ __typename me { __typename id } }`;
  const result = validateGraphQL(
    tenantSchema,
    policyFor('viewer', { blockIntrospection: false }),
    query,
  );
  assert.deepEqual(
    result.violations.map((violation) => violation.code),
    [],
  );
});

test('敏感租户作用域参数禁止客户端在字段上覆盖', () => {
  const query = `{ users(first: 5, tenantId: "evil-tenant") { id } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  const violation = result.violations.find(
    (item) => item.code === 'SENSITIVE_ARG_OVERRIDE',
  );
  assert.ok(violation);
  assert.equal(violation?.path, 'Query.users.tenantId');
});

test('嵌套输入对象中的敏感租户字段同样禁止覆盖', () => {
  const query = `
    mutation Hack($id: ID!) {
      updateUser(id: $id, scope: { tenantId: "t", region: "cn" }) { id }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('admin'), query);
  const violation = result.violations.find(
    (item) => item.code === 'SENSITIVE_ARG_OVERRIDE',
  );
  assert.ok(violation);
  assert.equal(violation?.path, 'Mutation.updateUser.scope.tenantId');
});

test('未声明变量报 VARIABLE_NOT_DEFINED', () => {
  const query = `{ user(id: $ghost) { id } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(result.violations.some((violation) => violation.code === 'VARIABLE_NOT_DEFINED'));
});

test('变量类型不匹配报 VARIABLE_TYPE_MISMATCH', () => {
  const query = `
    query Wrong($id: Int!) {
      user(id: $id) { id }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  const violation = result.violations.find(
    (item) => item.code === 'VARIABLE_TYPE_MISMATCH',
  );
  assert.ok(violation);
  assert.equal(violation?.path, 'Query.user.id');
});

test('可空变量传入非空参数且无默认值报 VARIABLE_REQUIRED', () => {
  const query = `
    query MaybeId($id: ID) {
      user(id: $id) { id }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(result.violations.some((violation) => violation.code === 'VARIABLE_REQUIRED'));
});

test('@include 使用非布尔变量报类型不匹配', () => {
  const query = `
    query BadCond($count: Int!) {
      me { name @include(if: $count) }
    }
  `;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(
    result.violations.some((violation) => violation.code === 'VARIABLE_TYPE_MISMATCH'),
  );
});

test('未知字段与未知类型分别报 UNKNOWN_FIELD / UNKNOWN_TYPE', () => {
  const unknownField = validateGraphQL(
    tenantSchema,
    policyFor('viewer'),
    `{ me { nonexistent } }`,
  );
  assert.ok(unknownField.violations.some((item) => item.code === 'UNKNOWN_FIELD'));

  const unknownType = validateGraphQL(
    tenantSchema,
    policyFor('viewer'),
    `{ me { ...Ghost } } fragment Ghost on Ghost { id }`,
  );
  assert.ok(unknownType.violations.some((item) => item.code === 'UNKNOWN_TYPE'));
});

test('viewer 角色白名单之外的顶层字段被拒绝', () => {
  const query = `{ loginAttempt(email: "a@x.com", password: "p") { success } }`;
  const result = validateGraphQL(tenantSchema, policyFor('viewer'), query);
  assert.ok(result.violations.some((violation) => violation.code === 'FIELD_FORBIDDEN'));
});
