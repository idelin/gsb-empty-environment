import * as assert from "assert";
import {
  BillingEngine,
  RateVersion,
  UsageEvent,
  monthOf,
} from "../src/billing";

const T = (day: number, hour = 0) => Date.UTC(2026, 0, day, hour); // 2026-01
const MONTH = "2026-01";
const CLOSE = Date.UTC(2026, 1, 1); // 账期关闭时间

// 费率 v1：1/1 起，前 100 单位 10 分，之后 20 分
// 费率 v2：1/15 起，前 100 单位 12 分，之后 25 分
const RATES: RateVersion[] = [
  {
    version: "v1",
    effectiveFrom: Date.UTC(2026, 0, 1),
    tiers: [
      { upTo: 100, unitPriceCents: 10 },
      { upTo: Infinity, unitPriceCents: 20 },
    ],
  },
  {
    version: "v2",
    effectiveFrom: Date.UTC(2026, 0, 15),
    tiers: [
      { upTo: 100, unitPriceCents: 12 },
      { upTo: Infinity, unitPriceCents: 25 },
    ],
  },
];

let seq = 0;
function ev(
  customerId: string,
  effectiveAt: number,
  quantity: number,
  recordedAt: number
): UsageEvent {
  return { id: `e${++seq}`, customerId, effectiveAt, quantity, recordedAt };
}

function newEngine() {
  return new BillingEngine(RATES);
}

/** 核心不变式：已出账单净额 == 一次性正确结算 */
function assertInvariant(engine: BillingEngine, customerId: string, month: string) {
  assert.strictEqual(
    engine.netBilledCents(customerId, month),
    engine.correctTotalCents(customerId, month),
    `net billed != one-shot settlement for ${customerId} ${month}`
  );
}

// ---------------------------------------------------------------- 1. 跨阶梯撤回
function testCrossTierRetraction() {
  const engine = newEngine();
  const c = "cust-cross-tier";

  // 80 + 60 = 140，跨过 100 的阶梯线：80*10 + 20*10 + 40*20 = 1800
  const e1 = ev(c, T(3), 80, T(3));
  const e2 = ev(c, T(10), 60, T(10));
  engine.recordEvent(e1);
  engine.recordEvent(e2);

  const bill = engine.closeMonth(c, MONTH, CLOSE);
  assert.strictEqual(bill.totalCents, 80 * 10 + 20 * 10 + 40 * 20);
  assert.strictEqual(bill.kind, "ORIGINAL");

  // 撤回 80 单位的事件：累计回落到 60，全部落在第一阶梯 => 600
  engine.retractEvent(e1.id, Date.UTC(2026, 1, 5));
  const adj = engine.recalculate(c, MONTH, Date.UTC(2026, 1, 5), "retract e1");
  assert.ok(adj, "expected an adjustment bill");
  assert.strictEqual(adj!.kind, "ADJUSTMENT");
  assert.strictEqual(adj!.totalCents, 600 - 1800); // 负差额 -1200

  assertInvariant(engine, c, MONTH);
  assert.strictEqual(engine.netBilledCents(c, MONTH), 600);
  console.log("ok 1. 跨阶梯撤回: 1800 -> 调整 -1200 -> 净额 600");
}

// ---------------------------------------------------------------- 2. 零用量
function testZeroUsage() {
  const engine = newEngine();
  const c = "cust-zero";

  // 2a. 整月无事件：应开出 0 元账单
  const bill = engine.closeMonth(c, MONTH, CLOSE);
  assert.strictEqual(bill.totalCents, 0);
  assert.deepStrictEqual(bill.items, []);
  // 无变化时重算不应产生调整账单
  assert.strictEqual(engine.recalculate(c, MONTH, CLOSE + 1), null);
  assertInvariant(engine, c, MONTH);

  // 2b. 有用量出账后全部撤回：调整回 0
  const c2 = "cust-zero-full-retract";
  const e1 = ev(c2, T(5), 50, T(5));
  engine.recordEvent(e1);
  const b2 = engine.closeMonth(c2, MONTH, CLOSE);
  assert.strictEqual(b2.totalCents, 500);
  engine.retractEvent(e1.id, Date.UTC(2026, 1, 3));
  const adj = engine.recalculate(c2, MONTH, Date.UTC(2026, 1, 3), "retract all");
  assert.ok(adj);
  assert.strictEqual(adj!.totalCents, -500);
  assertInvariant(engine, c2, MONTH);
  assert.strictEqual(engine.netBilledCents(c2, MONTH), 0);
  console.log("ok 2. 零用量: 空账期开 0 元账单; 全部撤回后净额回到 0");
}

// ---------------------------------------------------------------- 3. 多次更正
function testMultipleCorrections() {
  const engine = newEngine();
  const c = "cust-multi";

  const e1 = ev(c, T(2), 40, T(2));
  const e2 = ev(c, T(8), 30, T(8));
  engine.recordEvent(e1);
  engine.recordEvent(e2);
  const bill = engine.closeMonth(c, MONTH, CLOSE);
  assert.strictEqual(bill.totalCents, 700);

  // 更正 1：迟到事件（1 月生效，2 月才到）
  const late1 = ev(c, T(20), 90, Date.UTC(2026, 1, 2));
  engine.recordEvent(late1);
  const a1 = engine.recalculate(c, MONTH, Date.UTC(2026, 1, 2), "late event");
  assert.ok(a1);
  assertInvariant(engine, c, MONTH);

  // 更正 2：撤回早期事件
  engine.retractEvent(e1.id, Date.UTC(2026, 1, 4));
  const a2 = engine.recalculate(c, MONTH, Date.UTC(2026, 1, 4), "retract e1");
  assert.ok(a2);
  assertInvariant(engine, c, MONTH);

  // 更正 3：又一笔迟到事件
  const late2 = ev(c, T(25), 200, Date.UTC(2026, 1, 6));
  engine.recordEvent(late2);
  const a3 = engine.recalculate(c, MONTH, Date.UTC(2026, 1, 6), "late event 2");
  assert.ok(a3);
  assertInvariant(engine, c, MONTH);

  // 更正 4：无变化重算 => 不产生调整
  assert.strictEqual(engine.recalculate(c, MONTH, Date.UTC(2026, 1, 7)), null);

  // 账单链完整：1 张原始 + 3 张调整，序号连续
  const bills = engine.listBills(c, MONTH);
  assert.strictEqual(bills.length, 4);
  assert.deepStrictEqual(bills.map((b) => b.seq), [1, 2, 3, 4]);
  assert.deepStrictEqual(
    bills.map((b) => b.kind),
    ["ORIGINAL", "ADJUSTMENT", "ADJUSTMENT", "ADJUSTMENT"]
  );

  // 最终净额 == 一次性正确结算（120 单位 v2 区间: 30*10? 不对——按生效时间逐个算）
  // 有效事件: e2(1/8, 30, v1), late1(1/20, 90, v2), late2(1/25, 200, v2)
  // 累计: 30(v1@10) -> 120: 70*v2@12 + 20*v2@25 -> 320: 200*v2@25
  const expected = 30 * 10 + 70 * 12 + 20 * 25 + 200 * 25;
  assert.strictEqual(engine.correctTotalCents(c, MONTH), expected);
  assert.strictEqual(engine.netBilledCents(c, MONTH), expected);
  console.log(`ok 3. 多次更正: 4 张账单净额 ${expected} == 一次性结算`);
}

// ---------------------------------------------------------------- 4. 费率版本由生效时间决定
function testRateVersionByEffectiveTime() {
  const engine = newEngine();
  const c = "cust-rate";

  // 1/20 生效（v2）
  const newer = ev(c, T(20), 50, T(20));
  engine.recordEvent(newer);

  // 先出账（只有 newer 到达）：50 * 12 = 600
  const bill = engine.closeMonth(c, MONTH, CLOSE);
  assert.strictEqual(bill.totalCents, 600);

  // 1/10 生效（v1），但 2 月才到达 —— 必须用 v1 费率
  const lateOld = ev(c, T(10), 50, Date.UTC(2026, 1, 2));
  engine.recordEvent(lateOld);

  // 迟到事件按 effectiveAt=1/10 落在 v1，且排序在 newer 之前：
  // 50*v1@10 + 50*v2@12 = 500 + 600 = 1100，差额 +500
  const adj = engine.recalculate(c, MONTH, Date.UTC(2026, 1, 2), "late v1 event");
  assert.ok(adj);
  assert.strictEqual(adj!.totalCents, 500);
  const v1Item = adj!.items.find((it) => it.rateVersion === "v1");
  assert.ok(v1Item, "adjustment must contain a v1-priced line");
  assert.strictEqual(v1Item!.unitPriceCents, 10);
  assertInvariant(engine, c, MONTH);
  console.log("ok 4. 费率版本: 迟到事件按生效时间适用 v1 费率");
}

// ---------------------------------------------------------------- 5. 确定性随机不变式测试
function testFuzzInvariant() {
  // 简单可复现的 LCG
  let seed = 42;
  const rand = (n: number) => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed % n;
  };

  for (let round = 0; round < 50; round++) {
    const engine = newEngine();
    const c = `cust-fuzz-${round}`;
    const ids: string[] = [];
    let clock = T(1);
    const tick = () => (clock += 3600_000 + rand(3600_000));

    // 出账前随机记录一些事件
    const preCount = rand(6);
    for (let i = 0; i < preCount; i++) {
      const e = ev(c, T(1 + rand(28), rand(24)), 1 + rand(150), tick());
      engine.recordEvent(e);
      ids.push(e.id);
    }
    engine.closeMonth(c, MONTH, CLOSE);
    assertInvariant(engine, c, MONTH);

    // 出账后随机操作：迟到事件 / 撤回 / 重算
    for (let step = 0; step < 20; step++) {
      const op = rand(3);
      if (op === 0) {
        const e = ev(c, T(1 + rand(28), rand(24)), 1 + rand(150), tick());
        engine.recordEvent(e);
        ids.push(e.id);
      } else if (op === 1 && ids.length > 0) {
        const idx = rand(ids.length);
        const id = ids.splice(idx, 1)[0]!;
        engine.retractEvent(id, tick());
      } else {
        engine.recalculate(c, MONTH, tick(), `fuzz step ${step}`);
        assertInvariant(engine, c, MONTH);
      }
    }
    // 最终重算并校验
    engine.recalculate(c, MONTH, tick(), "final");
    assertInvariant(engine, c, MONTH);
  }
  console.log("ok 5. 随机不变式: 50 轮 x 混合迟到/撤回/重算，净额恒等于一次性结算");
}

// ---------------------------------------------------------------- 6. 边界与错误
function testEdgeCases() {
  const engine = newEngine();
  const c = "cust-edge";

  // 未出账就重算 => 报错
  assert.throws(() => engine.recalculate(c, MONTH, CLOSE), /not billed/);
  // 重复事件 id => 报错
  const e1 = ev(c, T(1), 10, T(1));
  engine.recordEvent(e1);
  assert.throws(() => engine.recordEvent(e1), /duplicate/);
  // 撤回未知事件 => 报错
  assert.throws(() => engine.retractEvent("nope", T(1)), /unknown/);
  // 重复撤回 => 幂等不报错
  engine.retractEvent(e1.id, T(2));
  engine.retractEvent(e1.id, T(3));
  // 重复关账 => 报错
  engine.closeMonth(c, MONTH, CLOSE);
  assert.throws(() => engine.closeMonth(c, MONTH, CLOSE), /already billed/);
  // 跨月事件互不影响
  assert.strictEqual(monthOf(Date.UTC(2026, 1, 1)), "2026-02");
  console.log("ok 6. 边界: 未出账重算/重复事件/未知撤回/重复关账均正确报错");
}

testCrossTierRetraction();
testZeroUsage();
testMultipleCorrections();
testRateVersionByEffectiveTime();
testFuzzInvariant();
testEdgeCases();
console.log("\n全部验证通过。");
