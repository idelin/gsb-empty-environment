import { CreditLedger } from '../src/credit-ledger.ts';

const ledger = new CreditLedger(1000);

console.log('=== 验收场景：追溯性信用额度占用流水账 ===\n');
console.log(`初始额度: ${ledger.initialLimit}`);

ledger.consume(10, 800);
console.log('时间10 消费 800');
ledger.consume(20, 150);
console.log('时间20 消费 150');
console.log(`当前违约检测: ${JSON.stringify(ledger.detectViolations())} （无违约）\n`);

console.log('时间30 追溯调整：将时间5至15的额度调整为 700');
ledger.adjustLimit({
  from: 5,
  to: 15,
  mode: 'set',
  value: 700,
  recordedAt: 30,
  reason: '追溯调低历史额度',
});

console.log('\n--- 违约警报 ---');
for (const v of ledger.detectViolations()) {
  console.log(`⚠️  时间${v.time} 超额：占用${v.usage} > 额度${v.limit}，超额差量 ${v.excess}`);
}

console.log('\n--- 审计日志 ---');
for (const entry of ledger.auditLog()) {
  console.log(
    `[audit#${entry.seq}] 状态=${entry.status} 触发事件=${entry.triggerEventId} 入账时间=${entry.recordedAt}`,
  );
  for (const v of entry.violations) {
    console.log(`    时间${v.time}: 占用${v.usage} / 额度${v.limit} / 超额${v.excess}`);
  }
}

console.log('\n--- 全量流水（只追加，不删除） ---');
for (const e of ledger.events()) {
  console.log(`  seq=${e.seq} recordedAt=${e.recordedAt} ${e.type} id=${e.id}`, JSON.stringify(e));
}
