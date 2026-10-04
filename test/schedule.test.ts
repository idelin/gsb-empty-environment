import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateSchedule, settleAmount, annuityPayment, interestOf, parseRate } from '../src/index.ts';
import type { LoanInput } from '../src/index.ts';

function sum(rows: readonly { principal: number }[]): number {
  return rows.reduce((acc, row) => acc + row.principal, 0);
}

test('零利率：平均分摊，最后一期吸收尾差', () => {
  const schedule = generateSchedule({ principal: 10000, periods: 3, monthlyRate: '0' });
  assert.deepEqual(
    schedule.rows.map((row) => row.payment),
    [3333, 3333, 3334],
  );
  assert.equal(sum(schedule.rows), 10000);
  assert.equal(schedule.rows[2]!.balance, 0);
  assert.equal(schedule.totalInterest, 0);
});

test('等额本息标准案例：10000 元分 12 期，月费率 0.5%', () => {
  const schedule = generateSchedule({ principal: 1_000_000, periods: 12, monthlyRate: '0.005' });
  assert.equal(schedule.rows.length, 12);
  // 前 11 期月供相同
  for (let i = 0; i < 11; i += 1) {
    assert.equal(schedule.rows[i]!.payment, 86066);
  }
  // 首期利息 = round(1000000 * 0.005) = 5000
  assert.equal(schedule.rows[0]!.interest, 5000);
  // 每期：月供 = 利息 + 本金（先息后本）
  for (const row of schedule.rows) {
    assert.equal(row.payment, row.interest + row.principal);
  }
  // 本金合计 = 原本金，期末余额为 0
  assert.equal(sum(schedule.rows), 1_000_000);
  assert.equal(schedule.rows[11]!.balance, 0);
  // 每期利息 = 上期末余额 * 费率（按分舍入）
  let balance = 1_000_000;
  for (const row of schedule.rows) {
    assert.equal(row.interest, interestOf(balance, parseRate('0.005')));
    balance = row.balance;
  }
});

test('所有输出金额均为整数分，无浮点近似', () => {
  const schedule = generateSchedule({ principal: 999_983, periods: 17, monthlyRate: '0.0073' });
  for (const row of schedule.rows) {
    for (const value of [row.payment, row.interest, row.principal, row.prepaidPrincipal, row.balance]) {
      assert.ok(Number.isInteger(value), `非整数金额: ${value}`);
    }
  }
  assert.equal(sum(schedule.rows), 999_983);
});

test('费率在指定期变更：按剩余本金与剩余期数重算月供', () => {
  const input: LoanInput = {
    principal: 120_000,
    periods: 12,
    monthlyRate: '0.01',
    rateChanges: [{ fromPeriod: 6, rate: '0.005' }],
  };
  const schedule = generateSchedule(input);
  assert.equal(schedule.rows.length, 12);
  assert.equal(schedule.rows[0]!.rate, '0.01');
  assert.equal(schedule.rows[4]!.rate, '0.01');
  assert.equal(schedule.rows[5]!.rate, '0.005');
  assert.equal(schedule.rows[11]!.rate, '0.005');
  // 变更后月供下降
  assert.ok(schedule.rows[5]!.payment < schedule.rows[4]!.payment);
  // 变更后第 6~11 期月供一致
  for (let i = 5; i < 11; i += 1) {
    assert.equal(schedule.rows[i]!.payment, schedule.rows[5]!.payment);
  }
  assert.equal(sum(schedule.rows), 120_000);
  assert.equal(schedule.rows[11]!.balance, 0);
});

test('提前还款-减额：期数不变，月供下降', () => {
  const input: LoanInput = {
    principal: 100_000,
    periods: 10,
    monthlyRate: '0.01',
    prepayments: [{ period: 3, amount: 20_000, mode: 'reduce' }],
  };
  const schedule = generateSchedule(input);
  assert.equal(schedule.rows.length, 10);
  assert.equal(schedule.rows[2]!.prepaidPrincipal, 20_000);
  assert.ok(schedule.rows[3]!.payment < schedule.rows[0]!.payment);
  assert.equal(sum(schedule.rows) + 20_000, 100_000);
  assert.equal(schedule.rows[9]!.balance, 0);
});

test('提前还款-缩期：月供不变，总期数缩短', () => {
  const input: LoanInput = {
    principal: 100_000,
    periods: 10,
    monthlyRate: '0.01',
    prepayments: [{ period: 3, amount: 30_000, mode: 'shorten' }],
  };
  const schedule = generateSchedule(input);
  assert.ok(schedule.rows.length < 10, `实际期数 ${schedule.rows.length}`);
  assert.equal(schedule.rows[2]!.prepaidPrincipal, 30_000);
  // 缩期后月供保持不变（最后一期除外）
  for (let i = 0; i < schedule.rows.length - 1; i += 1) {
    assert.equal(schedule.rows[i]!.payment, schedule.rows[0]!.payment);
  }
  assert.equal(sum(schedule.rows) + 30_000, 100_000);
  assert.equal(schedule.rows[schedule.rows.length - 1]!.balance, 0);
});

test('提前还款金额超过剩余本金时直接结清', () => {
  const schedule = generateSchedule({
    principal: 50_000,
    periods: 12,
    monthlyRate: '0.01',
    prepayments: [{ period: 2, amount: 999_999, mode: 'reduce' }],
  });
  assert.equal(schedule.rows.length, 2);
  assert.equal(schedule.rows[1]!.balance, 0);
});

test('结清金额反算：剩余本金 + 当期已计提利息', () => {
  const input: LoanInput = { principal: 100_000, periods: 10, monthlyRate: '0.01' };
  const schedule = generateSchedule(input);
  const settlement = settleAmount(input, 4);
  // 第 4 期期初余额 = 第 3 期期末余额
  assert.equal(settlement.principal, schedule.rows[2]!.balance);
  assert.equal(settlement.interest, interestOf(settlement.principal, parseRate('0.01')));
  assert.equal(settlement.payoff, settlement.principal + settlement.interest);
  // 首期即结清：全部本金 + 一期利息
  const first = settleAmount(input, 1);
  assert.equal(first.principal, 100_000);
  assert.equal(first.interest, 1000);
  assert.equal(first.payoff, 101_000);
});

test('结清反算与提前还款计划联动', () => {
  const input: LoanInput = {
    principal: 100_000,
    periods: 10,
    monthlyRate: '0.01',
    prepayments: [{ period: 2, amount: 30_000, mode: 'shorten' }],
  };
  const schedule = generateSchedule(input);
  const settlement = settleAmount(input, 3);
  assert.equal(settlement.principal, schedule.rows[1]!.balance);
  assert.equal(settlement.payoff, settlement.principal + settlement.interest);
});

test('费率上调：按剩余本金与期数重算月供，计划正常结清', () => {
  const schedule = generateSchedule({
    principal: 100_000,
    periods: 6,
    monthlyRate: '0.01',
    rateChanges: [{ fromPeriod: 3, rate: '0.05' }],
  });
  assert.equal(schedule.rows.length, 6);
  assert.ok(schedule.rows[2]!.payment > schedule.rows[0]!.payment);
  assert.equal(schedule.rows[5]!.balance, 0);
  assert.equal(sum(schedule.rows), 100_000);
});

test('非法输入校验', () => {
  assert.throws(() => generateSchedule({ principal: 0, periods: 3, monthlyRate: '0.01' }), /本金/);
  assert.throws(() => generateSchedule({ principal: 100, periods: 0, monthlyRate: '0.01' }), /期数/);
  assert.throws(() => generateSchedule({ principal: 100, periods: 3, monthlyRate: '-0.01' }), /不能为负/);
  assert.throws(
    () => generateSchedule({ principal: 100, periods: 3, monthlyRate: '0.01', rateChanges: [{ fromPeriod: 9, rate: '0.02' }] }),
    /费率变更期次无效/,
  );
});

test('年金月供与利息的 BigInt 精确性（大额本金）', () => {
  // 10 亿元（分），36 期，月费率 0.375%
  const payment = annuityPayment(100_000_000_000, 36, parseRate('0.00375'));
  assert.ok(Number.isSafeInteger(payment));
  const schedule = generateSchedule({ principal: 100_000_000_000, periods: 36, monthlyRate: '0.00375' });
  assert.equal(sum(schedule.rows), 100_000_000_000);
  assert.equal(schedule.rows[35]!.balance, 0);
});
