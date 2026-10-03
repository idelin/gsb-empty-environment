import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitemporalContract } from '../src/contract.ts';

/**
 * 验收场景：
 *  - 合同业务期 [1, 100)，约定总量 100。
 *  - 知悉 10：录入业务 50 交付 30。
 *  - 知悉 20：作废该交付，并录入业务 40 交付 60。
 *  - 对比在知悉 15 与知悉 25 查询业务 60 时的状态与完成度。
 */
function buildAcceptanceContract() {
  const contract = new BitemporalContract({
    id: 'C-001',
    periodStart: 1,
    periodEnd: 100,
    totalQuantity: 100,
  });
  const d1 = contract.recordDelivery(10, 50, 30); // 知悉10 录入 业务50 交付30
  contract.voidDelivery(20, d1); //                  知悉20 作废该交付
  contract.recordDelivery(20, 40, 60); //            知悉20 录入 业务40 交付60
  return { contract, d1 };
}

test('验收：知悉15 查询业务60 -> 履行中，完成度 30%', () => {
  const { contract } = buildAcceptanceContract();
  const snap = contract.snapshot(60, 15);
  assert.equal(snap.state, 'IN_PROGRESS');
  assert.equal(snap.completion, 30);
  assert.equal(snap.deliveredQuantity, 30);
  assert.deepEqual(snap.deliveries.map((d) => d.validTime), [50]);
});

test('验收：知悉25 查询业务60 -> 履行中，完成度 60%（旧交付已作废，新交付可见）', () => {
  const { contract } = buildAcceptanceContract();
  const snap = contract.snapshot(60, 25);
  assert.equal(snap.state, 'IN_PROGRESS');
  assert.equal(snap.completion, 60);
  assert.equal(snap.deliveredQuantity, 60);
  assert.deepEqual(snap.deliveries.map((d) => d.validTime), [40]);
});

test('验收：同一业务时间、不同知悉时间，状态与完成度正确演进', () => {
  const { contract } = buildAcceptanceContract();
  // 知悉 5：尚未录入任何交付 -> 生效，0%
  assert.deepEqual(
    [contract.stateAt(60, 5), contract.completionAt(60, 5)],
    ['EFFECTIVE', 0],
  );
  // 知悉 15：第一笔交付可见 -> 履行中，30%
  assert.deepEqual(
    [contract.stateAt(60, 15), contract.completionAt(60, 15)],
    ['IN_PROGRESS', 30],
  );
  // 知悉 25：作废+补录生效 -> 履行中，60%
  assert.deepEqual(
    [contract.stateAt(60, 25), contract.completionAt(60, 25)],
    ['IN_PROGRESS', 60],
  );
});

test('状态机沿业务时间演进：草案 -> 生效 -> 履行中 -> 终止', () => {
  const { contract } = buildAcceptanceContract();
  const k = 25; // 固定知悉时间
  assert.equal(contract.stateAt(0, k), 'DRAFT'); //        生效前
  assert.equal(contract.stateAt(1, k), 'EFFECTIVE'); //    生效，尚无交付
  assert.equal(contract.stateAt(39, k), 'EFFECTIVE'); //   交付(业务40)发生前
  assert.equal(contract.stateAt(40, k), 'IN_PROGRESS'); // 首笔交付发生
  assert.equal(contract.stateAt(99, k), 'IN_PROGRESS');
  assert.equal(contract.stateAt(100, k), 'TERMINATED'); // 业务期结束
  assert.equal(contract.stateAt(150, k), 'TERMINATED');
});

test('完成度只统计业务时间不晚于切片的可见交付', () => {
  const { contract } = buildAcceptanceContract();
  // 知悉15：业务30 在交付(业务50)之前 -> 0%
  assert.equal(contract.completionAt(30, 15), 0);
  // 知悉25：业务39 在交付(业务40)之前 -> 0%；业务40 -> 60%
  assert.equal(contract.completionAt(39, 25), 0);
  assert.equal(contract.completionAt(40, 25), 60);
  // 终止时点仍按可见交付计算完成度
  assert.equal(contract.completionAt(100, 25), 60);
});

test('补交历史交付会改变过去业务时间切片的状态', () => {
  const contract = new BitemporalContract({ id: 'C-002', periodStart: 1, periodEnd: 100, totalQuantity: 50 });
  // 知悉 30 补交一笔业务 10 的交付
  contract.recordDelivery(30, 10, 20);
  // 知悉 29 看业务 10：生效；知悉 30 看同一业务时间：履行中
  assert.equal(contract.stateAt(10, 29), 'EFFECTIVE');
  assert.equal(contract.stateAt(10, 30), 'IN_PROGRESS');
  assert.equal(contract.completionAt(10, 30), 40);
});

test('作废只影响作废知悉时间之后的查询（历史知悉时间仍可见）', () => {
  const { contract, d1 } = buildAcceptanceContract();
  const visible = (k: number) => contract.activeDeliveriesAt(k).map((d) => d.id);
  assert.deepEqual(visible(15), [d1]); // 作废前：可见
  assert.deepEqual(visible(19), [d1]);
  assert.deepEqual(visible(20), ['D2']); // 作废时点起不可见，新交付可见
  assert.deepEqual(visible(25), ['D2']);
});

test('完成度上限封顶 100%', () => {
  const contract = new BitemporalContract({ id: 'C-003', periodStart: 1, periodEnd: 100, totalQuantity: 50 });
  contract.recordDelivery(1, 10, 80); // 超量交付
  assert.equal(contract.completionAt(10, 1), 100);
  assert.equal(contract.deliveredQuantityAt(10, 1), 80);
});

test('非法输入与非法作废被拒绝', () => {
  const contract = new BitemporalContract({ id: 'C-004', periodStart: 1, periodEnd: 100, totalQuantity: 100 });
  assert.throws(() => contract.recordDelivery(1, 0, 10), RangeError); // 业务期外
  assert.throws(() => contract.recordDelivery(1, 100, 10), RangeError); // 业务期外（右开）
  assert.throws(() => contract.recordDelivery(1, 50, 0), RangeError); // 非正数量
  assert.throws(() => contract.voidDelivery(2, 'NOPE'), /不存在/);
  const id = contract.recordDelivery(10, 50, 10);
  assert.throws(() => contract.voidDelivery(5, id), RangeError); // 作废早于录入
  contract.voidDelivery(20, id);
  assert.throws(() => contract.voidDelivery(21, id), /已被作废/); // 重复作废
});

test('非法合同条款被拒绝', () => {
  assert.throws(() => new BitemporalContract({ id: 'X', periodStart: 100, periodEnd: 1, totalQuantity: 1 }), RangeError);
  assert.throws(() => new BitemporalContract({ id: 'X', periodStart: 1, periodEnd: 100, totalQuantity: 0 }), RangeError);
  assert.throws(
    () => new BitemporalContract({ id: 'X', periodStart: 1, periodEnd: 100, effectiveFrom: 200, totalQuantity: 1 }),
    RangeError,
  );
});
