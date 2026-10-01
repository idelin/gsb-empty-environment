import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  BitemporalContract,
  ContractState,
} from '../src/bitemporal-contract.ts';

/** 构造验收场景：知悉10录业务50交付30；知悉20作废并录业务40交付45 */
function acceptanceContract(): BitemporalContract {
  const c = new BitemporalContract(1, 100, 100);
  c.advanceKnowledgeTime(10);
  const id = c.recordDelivery(50, 30);
  c.advanceKnowledgeTime(20);
  c.voidDelivery(id);
  c.recordDelivery(40, 45);
  return c;
}

test('合同状态沿业务时间轴演进：草案 -> 生效 -> 履行中 -> 终止', () => {
  const c = acceptanceContract();
  assert.equal(c.stateAt(0.5, 25), ContractState.Draft);
  assert.equal(c.stateAt(1, 25), ContractState.Effective);
  assert.equal(c.stateAt(39, 25), ContractState.Effective);
  assert.equal(c.stateAt(40, 25), ContractState.Performing);
  assert.equal(c.stateAt(99, 25), ContractState.Performing);
  assert.equal(c.stateAt(100, 25), ContractState.Terminated);
});

test('验收边界：知悉15查询业务60 —— 作废尚未知悉，旧交付可见', () => {
  const c = acceptanceContract();
  const snap = c.snapshotAt(60, 15);
  assert.equal(snap.state, ContractState.Performing);
  assert.equal(snap.completionPercent, 30);
  assert.deepEqual(snap.visibleDeliveries.map((d) => d.id), [1]);
});

test('验收边界：知悉25查询业务60 —— 旧交付作废、新交付生效', () => {
  const c = acceptanceContract();
  const snap = c.snapshotAt(60, 25);
  assert.equal(snap.state, ContractState.Performing);
  assert.equal(snap.completionPercent, 45);
  assert.deepEqual(snap.visibleDeliveries.map((d) => d.id), [2]);
});

test('同一业务时间、不同知悉时间的切片互不影响（双时态可重现）', () => {
  const c = acceptanceContract();
  // 先查知悉25再查知悉15，结果应与直接查询一致
  c.snapshotAt(60, 25);
  assert.equal(c.completionAt(60, 15), 30);
  assert.equal(c.completionAt(60, 25), 45);
});

test('补交历史交付会改变过去业务切片的完成度，但不改变知悉切片', () => {
  const c = new BitemporalContract(1, 100, 100);
  c.advanceKnowledgeTime(10);
  c.recordDelivery(50, 30);
  // 知悉15时，业务45尚无交付
  assert.equal(c.stateAt(45, 15), ContractState.Effective);
  assert.equal(c.completionAt(45, 15), 0);
  // 知悉20补录业务40的交付后，业务45在知悉25下变为履行中
  c.advanceKnowledgeTime(20);
  c.recordDelivery(40, 45);
  assert.equal(c.stateAt(45, 25), ContractState.Performing);
  assert.equal(c.completionAt(45, 25), 45);
  // 但知悉15的历史切片保持不变
  assert.equal(c.stateAt(45, 15), ContractState.Effective);
});

test('完成度按可见交付累计并封顶 100', () => {
  const c = new BitemporalContract(1, 100, 100);
  c.advanceKnowledgeTime(5);
  c.recordDelivery(10, 60);
  c.recordDelivery(20, 60);
  assert.equal(c.completionAt(30, 5), 100);
  // 业务15只见第一份交付
  assert.equal(c.completionAt(15, 5), 60);
});

test('非法操作被拒绝', () => {
  const c = new BitemporalContract(1, 100, 100);
  assert.throws(() => c.recordDelivery(0, 10), /不在合同业务期/);
  assert.throws(() => c.recordDelivery(100, 10), /不在合同业务期/);
  assert.throws(() => c.recordDelivery(50, 0), /必须为正数/);
  assert.throws(() => c.voidDelivery(999), /不存在或已作废/);
  c.advanceKnowledgeTime(10);
  assert.throws(() => c.advanceKnowledgeTime(10), /单调递增/);
  const id = c.recordDelivery(50, 10);
  c.advanceKnowledgeTime(20);
  c.voidDelivery(id);
  assert.throws(() => c.voidDelivery(id), /不存在或已作废/);
});

test('构造参数校验', () => {
  assert.throws(() => new BitemporalContract(100, 1, 100), /业务期非法/);
  assert.throws(() => new BitemporalContract(1, 100, 0), /必须为正数/);
});
