import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LexError, parseGraphQL, ParseError } from '../src/index.ts';

test('parser 解析操作、变量、别名、参数、指令与片段', () => {
  const document = parseGraphQL(`
    # 带注释的查询
    query GetUser($id: ID! = "42", $show: Boolean!) {
      user(id: $id) {
        id
        displayName: name
        email @include(if: $show)
        ...UserFields
        ... on Node { id }
      }
    }
    fragment UserFields on User {
      roles
    }
  `);

  assert.equal(document.definitions.length, 2);
  const operation = document.definitions[0];
  assert.equal(operation.kind, 'OperationDefinition');
  if (operation.kind !== 'OperationDefinition') {
    return;
  }
  assert.equal(operation.operation, 'query');
  assert.equal(operation.name, 'GetUser');
  assert.equal(operation.variableDefinitions.length, 2);
  assert.deepEqual(
    operation.variableDefinitions.map((definition) => definition.variable),
    ['id', 'show'],
  );

  const userField = operation.selectionSet.selections[0];
  assert.equal(userField.kind, 'Field');
  if (userField.kind !== 'Field') {
    return;
  }
  assert.equal(userField.name, 'user');
  assert.equal(userField.args[0].value.kind, 'Variable');

  const aliasField = userField.selectionSet?.selections[1];
  assert.equal(aliasField?.kind, 'Field');
  if (aliasField?.kind === 'Field') {
    assert.equal(aliasField.alias, 'displayName');
    assert.equal(aliasField.name, 'name');
  }

  const spread = userField.selectionSet?.selections[3];
  assert.equal(spread?.kind, 'FragmentSpread');
  const inline = userField.selectionSet?.selections[4];
  assert.equal(inline?.kind, 'InlineFragment');
  if (inline?.kind === 'InlineFragment') {
    assert.equal(inline.typeCondition, 'Node');
  }
});

test('parser 支持块字符串、枚举与对象/列表字面量', () => {
  const document = parseGraphQL(`
    {
      loginAttempt(
        email: "a@example.com"
        password: """
          multi
          line
        """
      ) { success token }
    }
  `);
  const root = document.definitions[0];
  if (root.kind !== 'OperationDefinition') {
    throw new assert.AssertionError({ message: 'expected operation' });
  }
  const field = root.selectionSet.selections[0];
  if (field.kind !== 'Field') {
    throw new assert.AssertionError({ message: 'expected field' });
  }
  const password = field.args.find((argument) => argument.name === 'password');
  assert.equal(password?.value.kind, 'StringValue');
  if (password?.value.kind === 'StringValue') {
    assert.equal(password.value.value, 'multi\nline');
  }
});

test('词法错误抛出 LexError 并带行列号', () => {
  assert.throws(
    () => parseGraphQL('{ me { ^ } }'),
    (error: unknown) => error instanceof LexError,
  );
});

test('语法错误抛出 ParseError 并带行列号', () => {
  assert.throws(
    () => parseGraphQL('{ me { '),
    (error: unknown) => error instanceof ParseError,
  );
  assert.throws(
    () => parseGraphQL('{ me(x: } }'),
    (error: unknown) => error instanceof ParseError,
  );
});

test('嵌套深度保护在解析期拦截超深输入', () => {
  const deep = `${'{'.repeat(250)}id${'}'.repeat(250)}`;
  assert.throws(
    () => parseGraphQL(deep, 200),
    (error: unknown) => error instanceof ParseError,
  );
});
