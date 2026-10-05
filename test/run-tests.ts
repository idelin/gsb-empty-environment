import { strict as assert } from 'assert';
import {
  CartLine,
  Coupon,
  DiscountCoupon,
  ItemCoupon,
  MAX_COUPONS,
  Settlement,
  ThresholdCoupon,
  refundItem,
  settle,
} from '../src';

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`  ok - ${name}`);
  } catch (err) {
    failed++;
    console.error(`  FAIL - ${name}`);
    console.error(err);
  }
}

const item = (id: string, skuId: string, amountOffPerUnit: number, extra?: Partial<ItemCoupon>): ItemCoupon => ({
  id, type: 'ITEM', skuId, amountOffPerUnit, ...extra,
});
const disc = (id: string, ratePercent: number, extra?: Partial<DiscountCoupon>): DiscountCoupon => ({
  id, type: 'DISCOUNT', ratePercent, ...extra,
});
const th = (id: string, threshold: number, amountOff: number, extra?: Partial<ThresholdCoupon>): ThresholdCoupon => ({
  id, type: 'THRESHOLD', threshold, amountOff, ...extra,
});
const line = (skuId: string, unitPrice: number, quantity: number): CartLine => ({ skuId, unitPrice, quantity });

function assertStepChain(s: Settlement): void {
  let prev = s.originalTotal;
  let sum = 0;
  for (const step of s.steps) {
    assert.equal(step.amountBefore, prev, '步骤金额应当首尾相接');
    assert.equal(step.amountBefore - step.discount, step.amountAfter);
    prev = step.amountAfter;
    sum += step.discount;
  }
  assert.equal(prev, s.payable, '最后一步金额应等于实付');
  assert.equal(sum, s.totalDiscount, '步骤优惠之和应等于总优惠');
}

// ---------- 单品券 ----------
test('单品券：按件立减', () => {
  const s = settle([line('A', 500, 2)], [item('I1', 'A', 200)]);
  assert.equal(s.originalTotal, 1000);
  assert.equal(s.payable, 600);
  assert.deepEqual(s.couponIds, ['I1']);
  assertStepChain(s);
});

test('单品券：优惠不超过该行金额', () => {
  const s = settle([line('A', 500, 1)], [item('I1', 'A', 999)]);
  assert.equal(s.payable, 0);
  assertStepChain(s);
});

test('单品券：购物车无对应商品时该券不可用', () => {
  const s = settle([line('A', 500, 1)], [item('I1', 'B', 100)]);
  assert.equal(s.payable, 500);
  assert.deepEqual(s.couponIds, []);
});

// ---------- 折扣券 ----------
test('折扣券：逐行向下取整', () => {
  // 两行各 155 分，5 折：每行 floor(77.5)=77，合计 154；若按总额算是 155
  const s = settle([line('A', 155, 1), line('B', 155, 1)], [disc('D1', 50)]);
  assert.equal(s.payable, 154);
  assertStepChain(s);
  const step = s.steps[0];
  assert.equal(step.lines.length, 2);
  assert.equal(step.lines[0].amountAfter, 77);
  assert.equal(step.lines[1].amountAfter, 77);
});

test('折扣券：限定 SKU 范围', () => {
  const s = settle(
    [line('A', 1000, 1), line('B', 1000, 1)],
    [disc('D1', 50, { skuIds: ['A'] })],
  );
  assert.equal(s.payable, 1500);
  assertStepChain(s);
});

// ---------- 满减券 ----------
test('满减券：门槛按结算到本券时的当前应付判断', () => {
  // 总额 10000；8 折后 8000 < 9000，折扣+满减组合非法
  const s = settle(
    [line('A', 10000, 1)],
    [disc('D1', 80), th('T1', 9000, 1000)],
  );
  assert.equal(s.payable, 8000);
  assert.deepEqual(s.couponIds, ['D1']);
  assertStepChain(s);
});

test('满减券：门槛未达时单独也不可用', () => {
  const s = settle([line('A', 500, 1)], [th('T1', 1000, 100)]);
  assert.equal(s.payable, 500);
  assert.deepEqual(s.couponIds, []);
});

// ---------- 组合最优 ----------
test('组合：折扣与满减叠加优于单用', () => {
  const s = settle(
    [line('A', 20000, 1)],
    [disc('D1', 90), th('T1', 15000, 3000)],
  );
  // 20000 -> 18000 -> 15000
  assert.equal(s.payable, 15000);
  assert.deepEqual(s.couponIds, ['D1', 'T1']);
  assertStepChain(s);
});

test('组合：三种券叠加，顺序为单品->折扣->满减', () => {
  const s = settle(
    [line('A', 1000, 2), line('B', 8000, 1)],
    [
      item('I1', 'A', 100),   // A 行 2000 -> 1800
      disc('D1', 50),          // 全场半价：1800->900, 8000->4000
      th('T1', 4000, 900),     // 4900 >= 4000 -> 4000
    ],
  );
  assert.equal(s.payable, 4000);
  assert.deepEqual(s.couponIds, ['I1', 'D1', 'T1']);
  assertStepChain(s);
  assert.deepEqual(
    s.steps.map((x) => [x.amountBefore, x.discount, x.amountAfter]),
    [[10000, 200, 9800], [9800, 4900, 4900], [4900, 900, 4000]],
  );
});

// ---------- 互斥组 ----------
test('互斥组：同组券不可叠加，选更优者', () => {
  const s = settle(
    [line('A', 10000, 1)],
    [
      th('T1', 100, 5000, { mutexGroup: 'g' }),
      th('T2', 100, 4000, { mutexGroup: 'g' }),
    ],
  );
  assert.equal(s.payable, 5000);
  assert.deepEqual(s.couponIds, ['T1']);
  assertStepChain(s);
});

test('互斥组：不同组的券可以叠加', () => {
  const s = settle(
    [line('A', 10000, 1)],
    [
      th('T1', 100, 1000, { mutexGroup: 'g1' }),
      th('T2', 100, 1000, { mutexGroup: 'g2' }),
    ],
  );
  assert.equal(s.payable, 8000);
  assert.equal(s.couponIds.length, 2);
  assertStepChain(s);
});

// ---------- 先后关系 ----------
test('先后关系：缺少前置券时组合非法', () => {
  const s = settle(
    [line('A', 10000, 1)],
    [
      disc('D1', 90),
      th('T1', 5000, 500, { dependsOn: ['D1'] }),
    ],
  );
  // {T1} 非法；{D1,T1}: 9000 -> 8500 最优
  assert.equal(s.payable, 8500);
  assert.deepEqual(s.couponIds, ['D1', 'T1']);
  assertStepChain(s);
});

test('先后关系：依赖不存在的券在校验阶段报错', () => {
  assert.throws(() =>
    settle([line('A', 10000, 1)], [th('T1', 100, 9000, { dependsOn: ['NOPE'] })]),
  );
});

test('先后关系：显式依赖可改变结算顺序', () => {
  // 满减券 T1 依赖折扣券 D1：T1 虽类型靠后，但依赖关系保证 D1 先结算
  const s = settle(
    [line('A', 10000, 1)],
    [th('T1', 8000, 1000, { dependsOn: ['D1'] }), disc('D1', 90)],
  );
  assert.equal(s.payable, 8000); // 10000 -> 9000 -> 8000
  assert.deepEqual(s.couponIds, ['D1', 'T1']);
  assertStepChain(s);
});

// ---------- 并列规则 ----------
test('并列：实付相同取券数最少，再取 id 字典序', () => {
  const s = settle(
    [line('A', 1000, 1)],
    [
      th('T1', 1000, 200),
      th('T2', 1000, 200),
      th('T3', 900, 100),
      th('T4', 900, 100),
    ],
  );
  // {T1}、{T2}、{T3,T4} 都是 800（T1 用掉后余额不足 900，无法再叠 T3/T4）；
  // 券数最少的是 {T1}/{T2}，字典序取 T1
  assert.equal(s.payable, 800);
  assert.deepEqual(s.couponIds, ['T1']);
  assertStepChain(s);
});

// ---------- 退款 ----------
test('退款：退一件后整体重算而非按比例退券', () => {
  const cart = [line('A', 6000, 1), line('B', 5000, 1)];
  const coupons = [th('T1', 10000, 2000)];
  const r = refundItem(cart, coupons, 'B');
  assert.equal(r.before.payable, 9000); // 11000 - 2000
  assert.equal(r.after.payable, 6000);  // 退掉 B 后不满门槛，券不可用
  assert.equal(r.refundAmount, 3000);   // 不是 5000，也不是按比例
  assertStepChain(r.after);
});

test('退款：退部分数量', () => {
  const cart = [line('A', 1000, 3)];
  const r = refundItem(cart, [th('T1', 2000, 500)], 'A', 1);
  assert.equal(r.before.payable, 2500); // 3000 - 500
  assert.equal(r.after.payable, 1500);  // 2000 - 500
  assert.equal(r.refundAmount, 1000);
});

test('退款：数量不足时报错', () => {
  assert.throws(() => refundItem([line('A', 100, 1)], [], 'A', 2));
});

// ---------- 校验 ----------
test('校验：券数量超过 20 张报错', () => {
  const coupons = Array.from({ length: MAX_COUPONS + 1 }, (_, i) => th(`T${i}`, 1, 1));
  assert.throws(() => settle([line('A', 100, 1)], coupons));
});

test('校验：重复 id / 非法折扣率 / 依赖不存在的券', () => {
  assert.throws(() => settle([line('A', 100, 1)], [th('T1', 1, 1), th('T1', 1, 1)]));
  assert.throws(() => settle([line('A', 100, 1)], [disc('D1', 0)]));
  assert.throws(() => settle([line('A', 100, 1)], [disc('D1', 101)]));
  assert.throws(() =>
    settle([line('A', 100, 1)], [th('T1', 1, 1, { dependsOn: ['NOPE'] })]),
  );
});

test('校验：20 张券（含互斥组）可正常求解', () => {
  const coupons: Coupon[] = [];
  for (let i = 0; i < MAX_COUPONS; i++) {
    coupons.push(th(`T${i}`, 100, 10 + i, { mutexGroup: `g${i % 2}` }));
  }
  const s = settle([line('A', 10000, 1)], coupons);
  // 每组各取最优一张：g0 最大减 28（T18），g1 最大减 29（T19）
  assert.equal(s.payable, 10000 - 28 - 29);
  assert.equal(s.couponIds.length, 2);
  assertStepChain(s);
});

// ---------- 边界 ----------
test('边界：无券与空购物车', () => {
  const s = settle([line('A', 100, 1)], []);
  assert.equal(s.payable, 100);
  assert.equal(s.steps.length, 0);
  const empty = settle([], [th('T1', 1, 1)]);
  assert.equal(empty.payable, 0);
  assert.equal(empty.couponIds.length, 0);
});

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
