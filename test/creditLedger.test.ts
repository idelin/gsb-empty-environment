import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CreditLedger } from '../src/creditLedger.ts';

test('验收场景：追溯调低历史额度触发时间10超额100的违约警报', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 800);
  ledger.consume(20, 150);
  assert.equal(ledger.getViolations().length, 0, '调整前不应有违约');

  const result = ledger.adjustLimit(5, 15, 700, 30);
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.newViolations.length, 1);
    assert.deepEqual(result.newViolations[0], {
      time: 10,
      usage: 800,
      limit: 700,
      excess: 100,
    });
  }

  const violations = ledger.getViolations();
  assert.equal(violations.length, 1);
  assert.equal(violations[0].time, 10);
  assert.equal(violations[0].excess, 100);

  const alert = ledger.getAuditLog().find((e) => e.action === 'VIOLATION_ALERT');
  assert.ok(alert, '审计日志中应存在违约警报');
  assert.match(alert!.detail, /时间 10/);
  assert.match(alert!.detail, /超额 100/);
});

test('区间外的时刻不受追溯调整影响', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 800);
  ledger.consume(20, 150);
  ledger.adjustLimit(5, 15, 700, 30);
  assert.equal(ledger.getLimitAt(10), 700);
  assert.equal(ledger.getLimitAt(20), 1000, '时间20在调整区间外，仍为初始额度');
  assert.equal(ledger.getUsageAt(20), 950);
  assert.equal(ledger.getViolations().length, 1, '仅时间10违约，时间20占用950未超1000');
});

test('历史插入临时额度（调高）同样生效', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 1200);
  assert.equal(ledger.getViolations().length, 1, '消费时即超额');

  const result = ledger.adjustLimit(5, 15, 1500, 30);
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.equal(result.newViolations.length, 0, '追溯调高后不再产生新违约');
  }
  assert.equal(ledger.getLimitAt(10), 1500);
  assert.equal(
    ledger.getViolations().length,
    1,
    '已记录的违约警报不可物理删除，仍保留在账本中',
  );
});

test('重叠期冻结：与冻结区间重叠的调整被拒绝并记入审计日志', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 800);
  ledger.freeze(0, 50, '监管核查中');

  const result = ledger.adjustLimit(5, 15, 700, 30);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.match(result.reason, /冻结区间/);
  }
  assert.equal(ledger.getLimitAt(10), 1000, '被拒绝的调整不生效');
  assert.equal(ledger.getViolations().length, 0);

  const rejected = ledger.getAuditLog().find((e) => e.action === 'ADJUST_REJECTED_FROZEN');
  assert.ok(rejected, '拒绝行为应记入审计日志');

  const okResult = ledger.adjustLimit(60, 70, 500, 40);
  assert.equal(okResult.ok, true, '不与冻结区间重叠的调整应正常生效');
});

test('账本只追加不删除：消费与调整记录均保留完整审计轨迹', () => {
  const ledger = new CreditLedger(1000);
  ledger.consume(10, 800);
  ledger.consume(20, 150);
  ledger.adjustLimit(5, 15, 700, 30);

  const actions = ledger.getAuditLog().map((e) => e.action);
  assert.deepEqual(actions, ['INIT', 'CONSUME', 'CONSUME', 'ADJUST_LIMIT', 'VIOLATION_ALERT']);
});

test('多次调整同一区间时，最晚入账的调整生效', () => {
  const ledger = new CreditLedger(1000);
  ledger.adjustLimit(5, 15, 700, 30);
  ledger.adjustLimit(5, 15, 900, 40);
  assert.equal(ledger.getLimitAt(10), 900);
});

test('参数校验', () => {
  const ledger = new CreditLedger(1000);
  assert.throws(() => ledger.consume(1, 0), /正数/);
  assert.throws(() => ledger.adjustLimit(15, 5, 100, 30), /起点不能大于终点/);
  assert.throws(() => ledger.adjustLimit(5, 15, -1, 30), /负数/);
  assert.throws(() => ledger.freeze(10, 5), /起点不能大于终点/);
});
