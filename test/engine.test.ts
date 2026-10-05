import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MAX_COUPONS,
  applicationOrder,
  evaluate,
  settle,
  settleAfterReturn,
  validate,
  type CartItem,
  type Coupon,
} from '../src/index.ts';

const cart = (...items: Array<[string, number, number]>): CartItem[] =>
  items.map(([id, unitPrice, quantity]) => ({ id, name: id, unitPrice, quantity }));

test('满减券：达到门槛立减', () => {
  const c = cart(['a', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 't1', name: '满100减20', type: 'threshold', threshold: 10000, off: 2000 },
  ];
  const s = settle(c, coupons);
  assert.equal(s.payable, 8000);
  assert.equal(s.steps.length, 1);
  assert.equal(s.steps[0].amountBefore, 10000);
  assert.equal(s.steps[0].discount, 2000);
  assert.equal(s.steps[0].amountAfter, 8000);
});

test('满减券：未达门槛不使用', () => {
  const c = cart(['a', 9999, 1]);
  const coupons: Coupon[] = [
    { id: 't1', name: '满100减20', type: 'threshold', threshold: 10000, off: 2000 },
  ];
  const s = settle(c, coupons);
  assert.equal(s.payable, 9999);
  assert.equal(s.coupons.length, 0);
});

test('折扣券：逐行向下取整', () => {
  // 333 分打 5 折：floor(333*50/100)=166，应付 167
  const c = cart(['a', 333, 1], ['b', 101, 2]);
  const coupons: Coupon[] = [{ id: 'd1', name: '半价', type: 'discount', percent: 50 }];
  const s = settle(c, coupons);
  // a: 333-166=167; b 行总额 202, floor(202*0.5)=101 -> 101
  assert.equal(s.payable, 167 + 101);
  const step = s.steps[0];
  assert.deepEqual(
    step.lineBreakdown.map((b) => [b.itemId, b.off]),
    [['a', 166], ['b', 101]],
  );
});

test('单品券：按件立减且不超过行金额', () => {
  const c = cart(['a', 500, 3]);
  const coupons: Coupon[] = [
    { id: 'i1', name: '单品立减', type: 'item', itemId: 'a', amountOff: 100 },
  ];
  const s = settle(c, coupons);
  assert.equal(s.payable, 1500 - 300);
  const coupons2: Coupon[] = [
    { id: 'i2', name: '超额立减', type: 'item', itemId: 'a', amountOff: 600 },
  ];
  assert.equal(settle(c, coupons2).payable, 0);
});

test('互斥组：同组只取更优的一张', () => {
  const c = cart(['a', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 't1', name: '满100减20', type: 'threshold', threshold: 10000, off: 2000, mutexGroup: 'g' },
    { id: 't2', name: '满100减30', type: 'threshold', threshold: 10000, off: 3000, mutexGroup: 'g' },
  ];
  const s = settle(c, coupons);
  assert.equal(s.payable, 7000);
  assert.deepEqual(s.coupons.map((x) => x.id), ['t2']);
});

test('先后关系：依赖券未入选则本券不可用', () => {
  const c = cart(['a', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 'base', name: '满100减10', type: 'threshold', threshold: 10000, off: 1000 },
    { id: 'plus', name: '加码减5', type: 'threshold', threshold: 1, off: 500, requires: ['base'] },
  ];
  const s = settle(c, coupons);
  assert.deepEqual(s.coupons.map((x) => x.id).sort(), ['base', 'plus']);
  assert.equal(s.payable, 8500);
});

test('先后关系：前置券被互斥挤掉时，依赖券不能单独生效', () => {
  const c = cart(['a', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 'big', name: '立减30', type: 'threshold', threshold: 1, off: 3000, mutexGroup: 'g' },
    { id: 'base', name: '立减1', type: 'threshold', threshold: 1, off: 100, mutexGroup: 'g' },
    { id: 'plus', name: '加码减5', type: 'threshold', threshold: 1, off: 500, requires: ['base'] },
  ];
  // {big,plus} 不合法（plus 依赖 base，而 base 与 big 互斥）；最优为 {big}
  const s = settle(c, coupons);
  assert.deepEqual(s.coupons.map((x) => x.id), ['big']);
  assert.equal(s.payable, 7000);
});

test('并列实付：选用券最少者', () => {
  // {t1} 一张减 1000；{t2,t3} 两张各减 500；实付同为 9000，应选张数最少的 {t1}
  const c = cart(['a', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 't1', name: '满100减10', type: 'threshold', threshold: 10000, off: 1000 },
    { id: 't2', name: '满95减5', type: 'threshold', threshold: 9500, off: 500 },
    { id: 't3', name: '满90.01减5', type: 'threshold', threshold: 9001, off: 500 },
  ];
  const s = settle(c, coupons);
  assert.equal(s.payable, 9000);
  assert.equal(s.coupons.length, 1);
  assert.equal(s.coupons[0].id, 't1');
});

test('结算顺序：单品券 → 折扣券 → 满减券，门槛按折后金额判断', () => {
  const c = cart(['a', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 't', name: '满90减10', type: 'threshold', threshold: 9000, off: 1000 },
    { id: 'd', name: '9折', type: 'discount', percent: 90 },
    { id: 'i', name: '单品减5', type: 'item', itemId: 'a', amountOff: 500 },
  ];
  // 全部用上：10000-500=9500 → 9折减950 → 8550 < 9000 门槛不满足
  const all = evaluate(c, applicationOrder(coupons));
  assert.deepEqual(all.coupons.map((x) => x.type), ['item', 'discount', 'threshold']);
  assert.equal(all.payable, 8550);
  assert.equal(all.steps[2].discount, 0);
  // 但全局最优会放弃单品券：10000 → 9折 9000 → 满足门槛再减 1000 → 8000
  const best = settle(c, coupons);
  assert.deepEqual(best.coupons.map((x) => x.id), ['d', 't']);
  assert.equal(best.payable, 8000);
});

test('最多 20 张券', () => {
  const c = cart(['a', 100, 1]);
  const mk = (n: number): Coupon[] =>
    Array.from({ length: n }, (_, i) => ({
      id: `t${i}`, name: `t${i}`, type: 'threshold' as const, threshold: 1, off: 1,
    }));
  assert.equal(settle(c, mk(MAX_COUPONS)).payable, 100 - MAX_COUPONS);
  assert.throws(() => validate(c, mk(MAX_COUPONS + 1)), /最多/);
});

test('退货：重新计算最优组合而非按比例退券', () => {
  // 两件商品合计 15000 才能用满150减50；退掉一件后门槛不满足，券整体失效
  const c = cart(['a', 10000, 1], ['b', 5000, 1]);
  const coupons: Coupon[] = [
    { id: 't1', name: '满150减50', type: 'threshold', threshold: 15000, off: 5000 },
  ];
  const r = settleAfterReturn(c, coupons, 'b', 1);
  assert.equal(r.original.payable, 10000);
  assert.equal(r.afterReturn.payable, 10000); // 重算后无券可用
  assert.equal(r.refund, 0);
});

test('退货：重算后可能换用另一组券', () => {
  const c = cart(['a', 20000, 1], ['b', 10000, 1]);
  const coupons: Coupon[] = [
    { id: 'big', name: '满300减60', type: 'threshold', threshold: 30000, off: 6000, mutexGroup: 'm' },
    { id: 'small', name: '满200减30', type: 'threshold', threshold: 20000, off: 3000, mutexGroup: 'm' },
  ];
  const r = settleAfterReturn(c, coupons, 'b', 1);
  assert.deepEqual(r.original.coupons.map((x) => x.id), ['big']);
  assert.equal(r.original.payable, 24000);
  assert.deepEqual(r.afterReturn.coupons.map((x) => x.id), ['small']);
  assert.equal(r.afterReturn.payable, 17000);
  assert.equal(r.refund, 7000);
});

test('范围限定：券只作用于指定商品行', () => {
  const c = cart(['a', 10000, 1], ['b', 5000, 1]);
  const coupons: Coupon[] = [
    { id: 'd', name: 'a 专享 8 折', type: 'discount', percent: 80, itemIds: ['a'] },
  ];
  const s = settle(c, coupons);
  assert.equal(s.payable, 8000 + 5000);
});

test('逐步金额守恒：每步 before-discount=after，且首尾衔接', () => {
  const c = cart(['a', 3333, 2], ['b', 777, 3]);
  const coupons: Coupon[] = [
    { id: 'i', name: 'i', type: 'item', itemId: 'b', amountOff: 100 },
    { id: 'd', name: 'd', type: 'discount', percent: 88 },
    { id: 't', name: 't', type: 'threshold', threshold: 5000, off: 500 },
  ];
  const s = settle(c, coupons);
  let prev = s.totalBefore;
  for (const step of s.steps) {
    assert.equal(step.amountBefore, prev);
    assert.equal(step.amountBefore - step.discount, step.amountAfter);
    assert.equal(
      step.discount,
      step.lineBreakdown.reduce((sum, b) => sum + b.off, 0),
    );
    prev = step.amountAfter;
  }
  assert.equal(prev, s.payable);
});

test('evaluate 空组合原样返回', () => {
  const s = evaluate(cart(['a', 123, 4]), []);
  assert.equal(s.payable, 492);
  assert.equal(s.steps.length, 0);
});
