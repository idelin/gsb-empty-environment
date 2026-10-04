import { generateSchedule, settleAmount } from '../src/index.ts';

const input = {
  principal: 1_000_000, // 10000.00 元，单位：分
  periods: 12,
  monthlyRate: '0.005', // 月费率 0.5%
  rateChanges: [{ fromPeriod: 7, rate: '0.004' }], // 第 7 期起降息
  prepayments: [{ period: 4, amount: 100_000, mode: 'reduce' as const }], // 第 4 期提前还 1000 元，减额
};

const schedule = generateSchedule(input);
console.table(schedule.rows);
console.log('累计利息(分):', schedule.totalInterest, '累计还款(分):', schedule.totalPaid);
console.log('第 6 期期初结清:', settleAmount(input, 6));
