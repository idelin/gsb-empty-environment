import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReteEngine } from '../src/rete.ts';

function makeEngine(): { engine: ReteEngine; ruleName: string } {
  const engine = new ReteEngine();
  const ruleName = 'adult-with-big-order';
  engine.addRule({
    name: ruleName,
    conditions: [
      {
        type: 'User',
        fields: {
          age: { kind: 'const', value: 25 },
          id: { kind: 'var', name: 'uid' },
        },
      },
      {
        type: 'Order',
        fields: {
          amount: { kind: 'cmp', op: 'gte', value: 100 },
          userId: { kind: 'var', name: 'uid' },
        },
      },
    ],
  });
  return { engine, ruleName };
}

test('alpha 网络:常量过滤不通过的事实不会进入网络', () => {
  const { engine } = makeEngine();
  engine.addFact('User', { id: 1, age: 30 });
  engine.addFact('Order', { id: 9, userId: 1, amount: 500 });
  assert.equal(engine.getActivations().length, 0);
});

test('beta 网络:跨事实 join,变量不匹配不激活', () => {
  const { engine } = makeEngine();
  engine.addFact('User', { id: 1, age: 25 });
  engine.addFact('Order', { id: 9, userId: 2, amount: 500 });
  assert.equal(engine.getActivations().length, 0);
});

test('验收:注入 User(age=25) 与 Order(amount=100) 后规则增量激活', () => {
  const { engine, ruleName } = makeEngine();
  const userId = engine.addFact('User', { id: 1, age: 25 });
  assert.equal(engine.getActivations().length, 0, '仅有 User 时不应激活');

  const orderId = engine.addFact('Order', { id: 10, userId: 1, amount: 100 });
  const activations = engine.getActivations();
  assert.equal(activations.length, 1, '两条事实 join 成功后应激活一次');
  assert.equal(activations[0].rule, ruleName);
  assert.deepEqual(activations[0].bindings, { uid: 1 });
  assert.deepEqual(activations[0].factIds, [userId, orderId]);
});

test('验收:撤销 Order 后负 token 精准清除对应冲突集条目', () => {
  const { engine } = makeEngine();
  engine.addFact('User', { id: 1, age: 25 });
  const orderA = engine.addFact('Order', { id: 10, userId: 1, amount: 100 });
  const orderB = engine.addFact('Order', { id: 11, userId: 1, amount: 200 });
  assert.equal(engine.getActivations().length, 2);

  assert.equal(engine.retractFact(orderA), true);
  const remaining = engine.getActivations();
  assert.equal(remaining.length, 1, '只应清除被撤销 Order 的激活');
  assert.deepEqual(remaining[0].factIds[1], orderB);
  assert.equal(engine.hasFact(orderA), false);
  assert.equal(engine.hasFact(orderB), true);

  assert.equal(engine.retractFact(orderB), true);
  assert.equal(engine.getActivations().length, 0);
});

test('撤销 User 时沿 token 父链级联撤回全部派生激活', () => {
  const { engine } = makeEngine();
  const userId = engine.addFact('User', { id: 1, age: 25 });
  engine.addFact('Order', { id: 10, userId: 1, amount: 100 });
  engine.addFact('Order', { id: 11, userId: 1, amount: 300 });
  assert.equal(engine.getActivations().length, 2);

  engine.retractFact(userId);
  assert.equal(engine.getActivations().length, 0);
});

test('撤销不存在的事实返回 false 且不影响冲突集', () => {
  const { engine } = makeEngine();
  engine.addFact('User', { id: 1, age: 25 });
  engine.addFact('Order', { id: 10, userId: 1, amount: 100 });
  assert.equal(engine.retractFact(999), false);
  assert.equal(engine.getActivations().length, 1);
});

test('先注入事实再注册规则:基于 alpha 记忆增量激活,无需重扫事实库', () => {
  const engine = new ReteEngine();
  engine.addFact('User', { id: 1, age: 25 });
  engine.addFact('Order', { id: 10, userId: 1, amount: 100 });
  const { ruleName } = (() => {
    engine.addRule({
      name: 'late-rule',
      conditions: [
        { type: 'User', fields: { age: { kind: 'const', value: 25 }, id: { kind: 'var', name: 'uid' } } },
        { type: 'Order', fields: { amount: { kind: 'cmp', op: 'gte', value: 100 }, userId: { kind: 'var', name: 'uid' } } },
      ],
    });
    return { ruleName: 'late-rule' };
  })();
  const activations = engine.getActivations();
  assert.equal(activations.length, 1);
  assert.equal(activations[0].rule, ruleName);
});

test('单条件规则与多规则共享 alpha 节点', () => {
  const engine = new ReteEngine();
  engine.addRule({
    name: 'any-user-25',
    conditions: [{ type: 'User', fields: { age: { kind: 'const', value: 25 } } }],
  });
  engine.addRule({
    name: 'user-25-with-order',
    conditions: [
      { type: 'User', fields: { age: { kind: 'const', value: 25 }, id: { kind: 'var', name: 'uid' } } },
      { type: 'Order', fields: { userId: { kind: 'var', name: 'uid' } } },
    ],
  });
  const userId = engine.addFact('User', { id: 1, age: 25 });
  assert.deepEqual(
    engine.getActivations().map((a) => a.rule),
    ['any-user-25'],
  );
  const orderId = engine.addFact('Order', { id: 10, userId: 1, amount: 5 });
  assert.deepEqual(
    engine.getActivations().map((a) => a.rule),
    ['any-user-25', 'user-25-with-order'],
  );
  engine.retractFact(orderId);
  assert.deepEqual(
    engine.getActivations().map((a) => a.rule),
    ['any-user-25'],
  );
  engine.retractFact(userId);
  assert.equal(engine.getActivations().length, 0);
});
