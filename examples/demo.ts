import { ReteEngine } from '../src/rete.ts';

const engine = new ReteEngine();

engine.addRule({
  name: 'adult-with-big-order',
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

function show(step: string): void {
  console.log(`\n== ${step} ==`);
  const activations = engine.getActivations();
  if (activations.length === 0) {
    console.log('冲突集: (空)');
  }
  for (const a of activations) {
    console.log(
      `冲突集: 规则=${a.rule} 绑定=${JSON.stringify(a.bindings)} 事实=${JSON.stringify(a.factIds)}`,
    );
  }
}

const userId = engine.addFact('User', { id: 1, age: 25 });
show('注入 User(age=25)');

const orderId = engine.addFact('Order', { id: 10, userId: 1, amount: 100 });
show('注入 Order(amount=100)');

engine.retractFact(orderId);
show('撤销 Order(amount=100)');

console.log(`\nUser 事实仍存在: ${engine.hasFact(userId)}`);
