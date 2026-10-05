import test from 'node:test';
import assert from 'node:assert/strict';
import {
  AlreadyRevokedError,
  BenefitLedger,
  InsufficientQuotaError,
  UnknownBenefitTypeError,
} from '../src/index.ts';

const T0 = 1_000_000;
const DAY = 86_400_000;

function newLedger(): BenefitLedger {
  const ledger = new BenefitLedger();
  ledger.registerBenefitType('wash', 'stack');
  ledger.registerBenefitType('lounge', 'max');
  return ledger;
}

test('stack 规则：重叠套餐额度叠加', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 5 } });
  ledger.addPackage('m1', { packageId: 'p2', validFrom: T0, validTo: T0 + 60 * DAY, quotas: { wash: 3 } });
  assert.equal(ledger.getBalance('m1', 'wash', T0 + DAY).effective, 8);
});

test('max 规则：重叠套餐额度取最大', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { lounge: 5 } });
  ledger.addPackage('m1', { packageId: 'p2', validFrom: T0, validTo: T0 + 60 * DAY, quotas: { lounge: 3 } });
  assert.equal(ledger.getBalance('m1', 'lounge', T0 + DAY).effective, 5);
});

test('有效区间：未生效与已过期的套餐不计入额度（半开区间）', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 10 * DAY, quotas: { wash: 5 } });
  assert.equal(ledger.getBalance('m1', 'wash', T0 - 1).effective, 0);
  assert.equal(ledger.getBalance('m1', 'wash', T0).effective, 5);
  assert.equal(ledger.getBalance('m1', 'wash', T0 + 10 * DAY - 1).effective, 5);
  assert.equal(ledger.getBalance('m1', 'wash', T0 + 10 * DAY).effective, 0);
});

test('确定性扣减：stack 按先到期先用，同样的操作序列得到同样的分配', () => {
  const run = () => {
    const ledger = newLedger();
    ledger.addPackage('m1', { packageId: 'p-late', validFrom: T0, validTo: T0 + 60 * DAY, quotas: { wash: 4 } });
    ledger.addPackage('m1', { packageId: 'p-early', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 4 } });
    ledger.consume('m1', 'wash', 3, T0 + DAY);
    ledger.consume('m1', 'wash', 3, T0 + 2 * DAY);
    return ledger.getConsumptionHistory({ memberId: 'm1' });
  };
  const a = run();
  const b = run();
  assert.deepEqual(a, b);
  // 先扣先到期的 p-early，用尽后再扣 p-late
  assert.deepEqual(a[0].allocations, [{ packageId: 'p-early', amount: 3 }]);
  assert.deepEqual(a[1].allocations, [
    { packageId: 'p-early', amount: 1 },
    { packageId: 'p-late', amount: 2 },
  ]);
});

test('确定性扣减：max 规则扣在剩余最多的套餐上', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p-small', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { lounge: 2 } });
  ledger.addPackage('m1', { packageId: 'p-big', validFrom: T0, validTo: T0 + 60 * DAY, quotas: { lounge: 5 } });
  const record = ledger.consume('m1', 'lounge', 4, T0 + DAY);
  assert.deepEqual(record.allocations, [{ packageId: 'p-big', amount: 4 }]);
  assert.equal(ledger.getBalance('m1', 'lounge', T0 + DAY).effective, 2);
});

test('额度不足时拒绝消费且不产生任何记录', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 2 } });
  assert.throws(() => ledger.consume('m1', 'wash', 3, T0 + DAY), InsufficientQuotaError);
  assert.equal(ledger.getConsumptionHistory({ memberId: 'm1' }).length, 0);
  assert.equal(ledger.getBalance('m1', 'wash', T0 + DAY).effective, 2);
});

test('撤销：按原来源逐笔返还，余额恢复', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p-early', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 4 } });
  ledger.addPackage('m1', { packageId: 'p-late', validFrom: T0, validTo: T0 + 60 * DAY, quotas: { wash: 4 } });
  const record = ledger.consume('m1', 'wash', 6, T0 + DAY);
  assert.deepEqual(record.allocations, [
    { packageId: 'p-early', amount: 4 },
    { packageId: 'p-late', amount: 2 },
  ]);
  ledger.revoke(record.consumptionId, T0 + 2 * DAY);
  const balance = ledger.getBalance('m1', 'wash', T0 + 3 * DAY);
  assert.equal(balance.effective, 8);
  const byId = new Map(balance.packages.map((p) => [p.packageId, p]));
  assert.equal(byId.get('p-early')!.remaining, 4);
  assert.equal(byId.get('p-late')!.remaining, 4);
  const history = ledger.getConsumptionHistory({ memberId: 'm1' });
  assert.equal(history[0].status, 'revoked');
  assert.equal(history[0].revokedAt, T0 + 2 * DAY);
  // 撤销不改写原分配
  assert.deepEqual(history[0].allocations, record.allocations);
});

test('撤销不延长有效期：套餐过期后返还的额度不可再消费', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 10 * DAY, quotas: { wash: 3 } });
  const record = ledger.consume('m1', 'wash', 3, T0 + DAY);
  ledger.revoke(record.consumptionId, T0 + 20 * DAY);
  const balance = ledger.getBalance('m1', 'wash', T0 + 20 * DAY);
  assert.equal(balance.effective, 0);
  assert.equal(balance.packages[0].remaining, 3);
  assert.equal(balance.packages[0].valid, false);
  assert.throws(() => ledger.consume('m1', 'wash', 1, T0 + 20 * DAY), InsufficientQuotaError);
});

test('重复撤销被拒绝', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 10 * DAY, quotas: { wash: 1 } });
  const record = ledger.consume('m1', 'wash', 1, T0 + DAY);
  ledger.revoke(record.consumptionId, T0 + 2 * DAY);
  assert.throws(() => ledger.revoke(record.consumptionId, T0 + 3 * DAY), AlreadyRevokedError);
});

test('追溯更正额度：不改写已确认消费的分配，余额按新额度计算', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 10 } });
  const record = ledger.consume('m1', 'wash', 6, T0 + DAY);

  ledger.correctPackage('m1', 'p1', { quotas: { wash: 20 } }, T0 + 2 * DAY);
  let history = ledger.getConsumptionHistory({ memberId: 'm1' });
  assert.deepEqual(history[0].allocations, [{ packageId: 'p1', amount: 6 }]);
  assert.equal(ledger.getBalance('m1', 'wash', T0 + 3 * DAY).effective, 14);

  // 更正到低于已消费量：余额钳制为 0，历史分配依然不变，冲突被显式报告
  ledger.correctPackage('m1', 'p1', { quotas: { wash: 4 } }, T0 + 4 * DAY);
  history = ledger.getConsumptionHistory({ memberId: 'm1' });
  assert.deepEqual(history[0].allocations, record.allocations);
  assert.equal(ledger.getBalance('m1', 'wash', T0 + 5 * DAY).effective, 0);
  const conflicts = ledger.detectConflicts('m1');
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].reason, 'quota_below_consumed');
  assert.equal(conflicts[0].consumptionId, record.consumptionId);
});

test('追溯更正有效期：历史分配不变，冲突显式报告而非悄悄改写', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 5 } });
  const record = ledger.consume('m1', 'wash', 2, T0 + 10 * DAY);

  // 把有效期更正到消费发生之前结束
  ledger.correctPackage('m1', 'p1', { validTo: T0 + 5 * DAY }, T0 + 11 * DAY);
  const history = ledger.getConsumptionHistory({ memberId: 'm1' });
  assert.deepEqual(history[0].allocations, record.allocations);
  assert.equal(history[0].status, 'confirmed');

  const conflicts = ledger.detectConflicts('m1');
  assert.equal(conflicts.length, 1);
  assert.equal(conflicts[0].reason, 'not_valid_at_consumption');

  // 更正后的有效期对后续消费生效
  assert.throws(() => ledger.consume('m1', 'wash', 1, T0 + 10 * DAY), InsufficientQuotaError);
});

test('审计事件完整记录更正前后快照', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 10 } });
  ledger.correctPackage('m1', 'p1', { quotas: { wash: 8 } }, T0 + DAY);
  const corrected = ledger.getEvents().find((e) => e.kind === 'package_corrected');
  assert.ok(corrected && corrected.kind === 'package_corrected');
  assert.equal(corrected.before.quotas.wash, 10);
  assert.equal(corrected.after.quotas.wash, 8);
});

test('历史查询返回深拷贝，调用方无法篡改账本', () => {
  const ledger = newLedger();
  ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + 30 * DAY, quotas: { wash: 5 } });
  ledger.consume('m1', 'wash', 2, T0 + DAY);
  const history = ledger.getConsumptionHistory({ memberId: 'm1' });
  history[0].allocations[0].amount = 999;
  history[0].status = 'revoked';
  const again = ledger.getConsumptionHistory({ memberId: 'm1' });
  assert.equal(again[0].allocations[0].amount, 2);
  assert.equal(again[0].status, 'confirmed');
});

test('未注册的权益类型被拒绝', () => {
  const ledger = new BenefitLedger();
  assert.throws(
    () => ledger.addPackage('m1', { packageId: 'p1', validFrom: T0, validTo: T0 + DAY, quotas: { wash: 1 } }),
    UnknownBenefitTypeError,
  );
  assert.throws(() => ledger.consume('m1', 'wash', 1, T0), UnknownBenefitTypeError);
});
