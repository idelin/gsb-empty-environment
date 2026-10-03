import { BitemporalContract } from '../src/contract.ts';

// 验收场景复现：合同业务期 [1, 100)，约定总量 100
const contract = new BitemporalContract({
  id: 'C-001',
  periodStart: 1,
  periodEnd: 100,
  totalQuantity: 100,
});

const d1 = contract.recordDelivery(10, 50, 30); // 知悉10：录入 业务50 交付30
contract.voidDelivery(20, d1); //                 知悉20：作废该交付
contract.recordDelivery(20, 40, 60); //           知悉20：录入 业务40 交付60

console.log('=== 双时态切片对比：查询业务时间 60 ===');
for (const k of [5, 15, 25]) {
  const s = contract.snapshot(60, k);
  console.log(
    `知悉${String(k).padStart(2)} | 状态=${s.state.padEnd(11)} 完成度=${s.completion}% ` +
      `已履约=${s.deliveredQuantity} 可见交付=[${s.deliveries.map((d) => `业务${d.validTime}×${d.quantity}`).join(', ')}]`,
  );
}

console.log('\n=== 状态机沿业务时间演进（固定知悉25） ===');
for (const t of [0, 1, 39, 40, 99, 100]) {
  console.log(`业务${String(t).padStart(3)} | 状态=${contract.stateAt(t, 25)} 完成度=${contract.completionAt(t, 25)}%`);
}
