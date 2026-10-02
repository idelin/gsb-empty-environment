import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BitemporalPriceLedger, FOREVER } from '../src/price-ledger.ts';

/** 构造验收场景：10 分于业务 [0,20) 知悉于 2；15 分于业务 [5,10) 知悉于 8。 */
function buildAcceptanceLedger(): { ledger: BitemporalPriceLedger; rule10: number; rule15: number } {
  const ledger = new BitemporalPriceLedger();
  const rule10 = ledger.insert(10, 0, 20, 2);
  const rule15 = ledger.insert(15, 5, 10, 8);
  return { ledger, rule10, rule15 };
}

test('验收边界：同一业务时刻随知悉时间推进而后入为主', () => {
  const { ledger } = buildAcceptanceLedger();

  assert.equal(ledger.priceAt(7, 7), 10, '业务7 @ 知悉7 应得 10');
  assert.equal(ledger.priceAt(7, 8), 15, '业务7 @ 知悉8 应得 15');
  assert.equal(ledger.priceAt(10, 8), 10, '业务10 @ 知悉8 应得 10（15 的区间右开）');
});

test('业务区间左闭右开', () => {
  const { ledger } = buildAcceptanceLedger();

  assert.equal(ledger.priceAt(0, 7), 10, '左端点包含');
  assert.equal(ledger.priceAt(19, 7), 10);
  assert.equal(ledger.priceAt(20, 7), undefined, '右端点排除');
  assert.equal(ledger.priceAt(5, 8), 15, '覆盖区间左端点包含');
  assert.equal(ledger.priceAt(9, 8), 15);
  assert.equal(ledger.priceAt(10, 8), 10, '覆盖区间右端点排除，落回旧规则残余段');
});

test('知悉时间之前规则不可见', () => {
  const { ledger } = buildAcceptanceLedger();

  assert.equal(ledger.priceAt(7, 1), undefined, '知悉1 时系统尚不知任何规则');
  assert.equal(ledger.priceAt(7, 2), 10, '知悉2 起 10 分规则可见');
  assert.equal(ledger.priceAt(7, 7), 10, '知悉8 之前 15 分规则不可见');
});

test('覆盖只作用于重叠部分，旧规则残余段在新知悉视角下仍可见', () => {
  const { ledger } = buildAcceptanceLedger();

  assert.equal(ledger.priceAt(3, 8), 10, '[0,5) 残余段');
  assert.equal(ledger.priceAt(15, 8), 10, '[10,20) 残余段');

  const open = ledger.history().filter((s) => s.txTo === FOREVER);
  const openOfRule10 = open
    .filter((s) => s.priceCents === 10)
    .map((s) => [s.validFrom, s.validTo])
    .sort((a, b) => a[0] - b[0]);
  assert.deepEqual(openOfRule10, [[0, 5], [10, 20]], '10 分规则被分裂为两段开放区间');
});

test('撤销只影响撤销发生之后的知悉时间查询', () => {
  const { ledger, rule15 } = buildAcceptanceLedger();

  assert.equal(ledger.revoke(rule15, 12), true);

  assert.equal(ledger.priceAt(7, 8), 15, '撤销前的知悉视角不受影响');
  assert.equal(ledger.priceAt(7, 11), 15, '撤销时刻之前仍可见');
  assert.equal(ledger.priceAt(7, 12), undefined, '撤销时刻起不可见，且不复活被覆盖的旧价');
  assert.equal(ledger.priceAt(3, 12), 10, '其他规则不受影响');
});

test('撤销未知规则返回 false', () => {
  const { ledger } = buildAcceptanceLedger();
  assert.equal(ledger.revoke(999, 12), false);
});

test('参数校验', () => {
  const ledger = new BitemporalPriceLedger();

  assert.throws(() => ledger.insert(10, 10, 10, 0), RangeError, '空业务区间');
  assert.throws(() => ledger.insert(10, 20, 10, 0), RangeError, '倒置业务区间');
  assert.throws(() => ledger.insert(10.5, 0, 10, 0), RangeError, '价格必须为整数分');
  assert.throws(() => ledger.insert(-1, 0, 10, 0), RangeError, '价格不可为负');
  assert.throws(() => ledger.insert(10, 0, 10, -1), RangeError, '知悉时间不可为负');

  ledger.insert(10, 0, 10, 5);
  assert.throws(() => ledger.insert(12, 0, 10, 4), RangeError, '知悉时间不可回退');
});
