import { test } from "node:test";
import assert from "node:assert/strict";
import { ConstantProductPool } from "../src/amm.ts";
import { TwapOracle, OracleError, ORACLE_CAPACITY } from "../src/oracle.ts";
import { Rat } from "../src/rational.ts";

const ratEq = (a: Rat, b: Rat) => a.cmp(b) === 0;

test("精确有理累加：累计值等于参考有理和", () => {
  const pool = new ConstantProductPool(1_000_000n, 2_000_000n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  pool.swap(100_000n, "oneForZero", 10n); // 触发 update(10)，用旧价格 2/1
  const obs = oracle.latest()!;
  assert.equal(obs.time, 10n);
  assert.ok(ratEq(obs.cumulative0, Rat.of(2n).scale(10n)));
  assert.ok(ratEq(obs.cumulative1, Rat.of(1n, 2n).scale(10n)));
});

test("环形缓冲覆盖：最多 64 条，最旧被淘汰", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  assert.equal(ORACLE_CAPACITY, 64);
  for (let t = 0n; t < 70n; t++) oracle.update(t);
  assert.equal(oracle.length, 64);
  const obs = oracle.observations();
  assert.equal(obs[0]!.time, 6n);
  assert.equal(obs[63]!.time, 69n);
  const reachable = oracle.consult(69n, 63n); // target=6 恰为最旧一条
  assert.ok(ratEq(reachable.price0, Rat.of(1n)));
  assert.equal(reachable.duration, 63n);
  assert.throws(
    () => oracle.consult(69n, 64n), // 需要已被淘汰的观察
    (e) => e instanceof OracleError && e.code === "INSUFFICIENT_HISTORY",
  );
});

test("同一时间多笔交换：dt=0 累计不变", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  pool.swap(10_000n, "oneForZero", 10n);
  pool.swap(20_000n, "zeroForOne", 10n);
  pool.swap(5_000n, "oneForZero", 10n);
  const at10 = oracle.observations().filter((o) => o.time === 10n);
  assert.equal(at10.length, 3);
  for (const o of at10) {
    assert.ok(ratEq(o.cumulative0, Rat.of(10n))); // 价格 1 * 10
  }
  const r = oracle.consult(20n, 10n);
  assert.equal(r.from.time, 10n);
  assert.equal(r.duration, 10n);
});

test("窗口边界：time = now-W 的观察恰可用", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  oracle.update(10n);
  const exact = oracle.consult(20n, 10n); // target=10，恰有观察
  assert.equal(exact.duration, 10n);
  assert.ok(ratEq(exact.price0, Rat.of(1n)));
  const wider = oracle.consult(20n, 20n); // target=0
  assert.equal(wider.duration, 20n);
  assert.throws(
    () => oracle.consult(20n, 21n), // target=-1，无观察
    (e) => e instanceof OracleError && e.code === "INSUFFICIENT_HISTORY",
  );
  assert.throws(
    () => oracle.consult(20n, 0n),
    (e) => e instanceof OracleError && e.code === "INVALID_WINDOW",
  );
});

test("时钟倒退拒绝", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  oracle.update(10n);
  assert.throws(
    () => oracle.update(5n),
    (e) => e instanceof OracleError && e.code === "CLOCK_REGRESSION",
  );
  assert.throws(
    () => oracle.consult(9n, 1n),
    (e) => e instanceof OracleError && e.code === "CLOCK_REGRESSION",
  );
});

test("价格分母为零拒绝", () => {
  const pool = new ConstantProductPool(100n, 100n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n); // 先在有效储备下落一条观察
  pool.x = 0n;
  assert.throws(
    () => oracle.update(1n),
    (e) => e instanceof OracleError && e.code === "ZERO_PRICE_DENOMINATOR",
  );
  assert.throws(
    () => oracle.consult(1n, 1n),
    (e) => e instanceof OracleError && e.code === "ZERO_PRICE_DENOMINATOR",
  );
});

test("长期真实价格变化：TWAP 等于测试侧精确有理参考且滞后于现货", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  // 测试侧独立参考环：每个时间步在交换前记录累计值
  const refCumAt: Rat[] = [Rat.zero()];
  const spotAt: Rat[] = [Rat.of(1n)];
  let refCum = Rat.zero();
  for (let t = 1n; t <= 100n; t++) {
    refCum = refCum.add(Rat.of(pool.y, pool.x)); // dt = 1
    refCumAt.push(refCum);
    pool.swap(10_000n, "oneForZero", t); // 每步推高价格
    spotAt.push(Rat.of(pool.y, pool.x));
  }
  const r = oracle.consult(100n, 50n);
  const expected = refCumAt[100]!.sub(refCumAt[50]!).div(Rat.of(50n));
  assert.ok(ratEq(r.price0, expected), "TWAP 等于精确有理参考");
  // 价格单调上行时：spot(50) < TWAP < spot(100)，TWAP 滞后于现货
  assert.ok(r.price0.cmp(spotAt[50]!) > 0);
  assert.ok(r.price0.cmp(spotAt[100]!) < 0);
});
