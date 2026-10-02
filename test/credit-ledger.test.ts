import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CreditLedger } from '../src/credit-ledger.ts';

test('验收场景：追溯调低历史额度后识别出时间10超额100并记录审计日志', () => {
  const ledger = new CreditLedger(1000);

  ledger.consume(10, 800); // 时间10消费800
  ledger.consume(20, 150); // 时间20消费150
  assert.deepEqual(ledger.detectViolations(), []); // 调整前无违约
  assert.equal(ledger.auditLog().length, 0);

  // 时间30追溯将时间5至15的额度调整为700
  const adjust = ledger.adjustLimit({
    from: 5,
    to: 15,
    mode: 'set',
    value: 700,
    recordedAt: 30,
    reason: '追溯调低历史额度',
  });

  // 系统自动识别时间10超额100
  assert.deepEqual(ledger.detectViolations(), [
    { time: 10, usage: 800, limit: 700, excess: 100 },
  ]);

  // 审计日志已记录，且由该追溯调整触发
  const log = ledger.auditLog();
  assert.equal(log.length, 1);
  assert.equal(log[0].status, 'ALERT');
  assert.equal(log[0].triggerEventId, adjust.id);
  assert.equal(log[0].recordedAt, 30);
  assert.deepEqual(log[0].violations, [
    { time: 10, usage: 800, limit: 700, excess: 100 },
  ]);

  // 期间外额度不受影响
  assert.equal(ledger.limitAt(4), 1000);
  assert.equal(ledger.limitAt(10), 700);
  assert.equal(ledger.limitAt(15), 1000); // [from, to) 左闭右开
  // 时间20占用950 < 1000，不违约
  assert.equal(ledger.usageAt(20), 950);
});

test('重叠期冻结：同一时段多笔冻结效果叠加', () => {
  const ledger = new CreditLedger(1000);
  ledger.freeze(10, 30, 400);
  ledger.freeze(20, 40, 300); // 与上一笔在 [20,30) 重叠

  assert.equal(ledger.usageAt(15), 400);
  assert.equal(ledger.usageAt(25), 700); // 重叠区叠加
  assert.equal(ledger.usageAt(35), 300);

  // 追加消费使重叠区超额：25 时刻 700(冻结) + 350(消费) = 1050 > 1000
  ledger.consume(5, 350);
  const violations = ledger.detectViolations();
  assert.deepEqual(violations, [{ time: 20, usage: 1050, limit: 1000, excess: 50 }]);
});

test('临时额度：历史期间插入临时额度提升（delta）', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 1200); // 超出基准额度
  ledger.release(14, 1200); // 时间14归还，占用仅存在于 [10,14)
  assert.equal(ledger.detectViolations().length, 1);

  // 追溯插入 [5,15) 临时额度 +500
  ledger.adjustLimit({ from: 5, to: 15, mode: 'delta', value: 500 });
  assert.equal(ledger.limitAt(10), 1500);
  assert.equal(ledger.limitAt(20), 1000);
  assert.deepEqual(ledger.detectViolations(), []); // 违约消除
  const log = ledger.auditLog();
  assert.equal(log.at(-1)!.status, 'RESOLVED');
});

test('历史冲正：冲正历史消费后违约消失，原始记录不物理删除', () => {
  const ledger = new CreditLedger(1000);
  const c1 = ledger.consume(10, 800);
  ledger.consume(20, 150);
  ledger.adjustLimit({ from: 5, to: 15, mode: 'set', value: 700 });
  assert.equal(ledger.detectViolations().length, 1);

  const eventCountBefore = ledger.events().length;
  ledger.reverse(c1.id, { reason: '历史冲正：该消费录入有误' });

  // 违约被追溯消除
  assert.deepEqual(ledger.detectViolations(), []);
  // 记录未物理删除：原始消费、冲正记录均在账本中
  const events = ledger.events();
  assert.equal(events.length, eventCountBefore + 1);
  assert.ok(events.some((e) => e.id === c1.id && e.type === 'CONSUME'));
  assert.ok(events.some((e) => e.type === 'REVERSAL' && e.targetId === c1.id));
  // 冲正后占用回溯为零
  assert.equal(ledger.usageAt(10), 0);
  // 审计日志记录解除
  assert.equal(ledger.auditLog().at(-1)!.status, 'RESOLVED');
});

test('冲正之冲正：恢复被冲正的事件效果', () => {
  const ledger = new CreditLedger(1000);
  const c1 = ledger.consume(10, 800);
  const r1 = ledger.reverse(c1.id);
  assert.equal(ledger.usageAt(10), 0);
  ledger.reverse(r1.id); // 撤销冲正
  assert.equal(ledger.usageAt(10), 800);
});

test('冲正额度调整：追溯调整被冲正后额度恢复', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 800);
  const adj = ledger.adjustLimit({ from: 5, to: 15, mode: 'set', value: 700 });
  assert.equal(ledger.detectViolations().length, 1);
  ledger.reverse(adj.id);
  assert.equal(ledger.limitAt(10), 1000);
  assert.deepEqual(ledger.detectViolations(), []);
});

test('释放与参数校验', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 800);
  ledger.release(20, 300);
  assert.equal(ledger.usageAt(20), 500);
  assert.throws(() => ledger.consume(1, 0), /amount must be > 0/);
  assert.throws(() => ledger.freeze(10, 5, 100), /from < to/);
  assert.throws(() => ledger.reverse('no-such-id'), /unknown event/);
});
