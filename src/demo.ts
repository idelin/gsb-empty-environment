/**
 * 运行样例：验收边界场景
 *
 * 合同业务期 [1, 100)，要求交付总量 100。
 *  知悉10：补录业务50的交付 30 单位
 *  知悉20：作废该交付，并补录业务40的交付 45 单位
 *  对比：知悉15 vs 知悉25 查询业务60 时的合同状态与完成度
 */
import { BitemporalContract, type BitemporalSnapshot } from './bitemporal-contract.ts';

const STATE_LABEL: Record<string, string> = {
  DRAFT: '草案',
  EFFECTIVE: '生效',
  PERFORMING: '履行中',
  TERMINATED: '终止',
};

function show(snap: BitemporalSnapshot): void {
  const deliveries = snap.visibleDeliveries
    .map((d) => `#${d.id}(业务${d.businessTime}, ${d.quantity}单位)`)
    .join(', ') || '无';
  console.log(
    `  业务时间 ${String(snap.businessTime).padStart(3)} | 知悉时间 ${String(snap.knowledgeTime).padStart(3)}` +
    ` => 状态: ${STATE_LABEL[snap.state]} | 完成度: ${snap.completionPercent}% | 可见交付: ${deliveries}`,
  );
}

const contract = new BitemporalContract(1, 100, 100);

console.log('== 知悉时间 10：补录业务50的交付 30 单位 ==');
contract.advanceKnowledgeTime(10);
const firstId = contract.recordDelivery(50, 30);
console.log(`  已录入交付凭证 #${firstId}`);

console.log('== 知悉时间 20：作废该交付，补录业务40的交付 45 单位 ==');
contract.advanceKnowledgeTime(20);
contract.voidDelivery(firstId);
const secondId = contract.recordDelivery(40, 45);
console.log(`  已作废交付凭证 #${firstId}，已录入交付凭证 #${secondId}`);

console.log('\n== 验收对比：查询业务60，知悉15 vs 知悉25 ==');
show(contract.snapshotAt(60, 15)); // 作废尚未发生：履行中，30%
show(contract.snapshotAt(60, 25)); // 作废+新交付生效：履行中，45%

console.log('\n== 状态沿业务时间轴演进（知悉25） ==');
show(contract.snapshotAt(0.5, 25));  // 草案
show(contract.snapshotAt(10, 25));   // 生效（业务40的交付尚未到达）
show(contract.snapshotAt(60, 25));   // 履行中
show(contract.snapshotAt(100, 25));  // 终止
