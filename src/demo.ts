import { CreditLedger } from './creditLedger.ts';

const ledger = new CreditLedger(1000);

console.log('=== 验收场景 ===');
ledger.consume(10, 800);
ledger.consume(20, 150);
console.log('时间 10 消费 800，时间 20 消费 150，当前无违约。');

const result = ledger.adjustLimit(5, 15, 700, 30);
console.log('\n时间 30 追溯将区间 [5, 15] 额度调整为 700：');
if (result.ok) {
  for (const v of result.newViolations) {
    console.log(
      `  [违约警报] 时间 ${v.time} 占用 ${v.usage} 超过额度 ${v.limit}，超额 ${v.excess}`,
    );
  }
}

console.log('\n=== 审计日志 ===');
for (const entry of ledger.getAuditLog()) {
  console.log(`  #${entry.seq} [${entry.action}] ${entry.detail}`);
}

console.log('\n=== 冻结演示 ===');
ledger.freeze(0, 50, '监管核查中');
const rejected = ledger.adjustLimit(10, 20, 500, 40);
if (!rejected.ok) {
  console.log(`  调整被拒绝：${rejected.reason}`);
}
