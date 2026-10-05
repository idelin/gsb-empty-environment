import { CommissionEngine } from '../src/index.ts';

const yuan = (cents: number): string => `¥${(cents / 100).toFixed(2)}`;

// 归属图：A 的上级是 B、C；B 和 C 的共同上级是 D（菱形，D 可经两条路径到达）
const engine = new CommissionEngine();
engine.addAgent('B', { monthlyCap: 120 });
engine.addAgent('D', { monthlyCap: 100 });
engine.setUpline('A', 'B', 0.2);
engine.setUpline('A', 'C', 0.3);
engine.setUpline('B', 'D', 0.5);
engine.setUpline('C', 'D', 0.4);

engine.distribute({ txId: 'T1', source: 'A', amount: 1000, month: '2026-10' });
engine.distribute({ txId: 'T2', source: 'A', amount: 500, month: '2026-10' });
engine.distribute({ txId: 'T3', source: 'A', amount: 800, month: '2026-11' });

console.log('== T1 分账记录（含完整来源链） ==');
for (const r of engine.allRecords().filter((r) => r.txId === 'T1')) {
  console.log(
    `${r.recipient}: 应分 ${yuan(r.grossCents)} 实付 ${yuan(r.paidCents)} 待补 ${yuan(r.pendingCents)}`,
  );
  for (const c of r.chains) {
    const mark = c === r.effectiveChain ? '（计酬链）' : '';
    console.log(`   链 ${c.path.join(' -> ')} 费率 ${c.rate}${mark}`);
  }
}

console.log('\n== 撤单 T1（释放 2026-10 封顶额度，按原交易顺序补发） ==');
for (const e of engine.cancel('T1', '用户退款')) {
  console.log(`#${e.seq} ${e.kind} ${e.agent} ${yuan(e.amountCents)} tx=${e.txId}`);
}

console.log('\n== B 的 2026-10 对账单 ==');
const st = engine.statement('B', '2026-10');
console.log(
  `应分 ${yuan(st.grossCents)} 净实付 ${yuan(st.netPaidCents)} 冲销 ${yuan(st.reversedCents)} 待补 ${yuan(st.pendingCents)} 封顶 ${st.capCents === null ? '无' : yuan(st.capCents)}`,
);

console.log('\n== 逐月金额守恒核对 ==');
for (const row of engine.reconcile()) {
  console.log(
    `${row.month}: gross=${yuan(row.grossCents)} = netPaid=${yuan(row.netPaidCents)} + reversed=${yuan(row.reversedCents)} + pending=${yuan(row.pendingCents)} + voided=${yuan(row.voidedCents)}  conserved=${row.conserved} 超封顶=${row.capViolations.length === 0 ? '无' : row.capViolations.join(',')}`,
  );
}
