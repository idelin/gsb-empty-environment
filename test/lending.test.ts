import { test } from "node:test";
import assert from "node:assert/strict";
import { ConstantProductPool } from "../src/amm.ts";
import { TwapOracle } from "../src/oracle.ts";
import {
  LendingHost,
  LendingError,
  spotPendingFeed,
  twapFeed,
} from "../src/lending.ts";
import { Rat } from "../src/rational.ts";

const makeHost = (
  pool: ConstantProductPool,
  feed: ReturnType<typeof spotPendingFeed>,
  overrides: Partial<{
    collateralFactor: Rat;
    maxObservationAge: bigint;
    minLiquidity: bigint;
  }> = {},
) =>
  new LendingHost({
    pool,
    feed,
    collateralFactor: overrides.collateralFactor ?? Rat.of(1n, 2n),
    maxObservationAge: overrides.maxObservationAge ?? 10_000n,
    minLiquidity: overrides.minLiquidity ?? 0n,
  });

test("同一交易先固定回执：之后的池变化不影响本次抵押率检查", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const host = makeHost(pool, spotPendingFeed(pool));
  const receipt = host.beginTx(100n);
  assert.equal(receipt.price.cmp(Rat.of(1n)), 0);
  host.deposit(1_000n);
  // 回执固定后操纵池价到约 4 倍
  pool.swap(1_000_000n, "oneForZero", 100n);
  // 仍按回执价格 1 检查：上限 = 1000 * 1 * 1/2 = 500
  assert.equal(host.maxBorrow(receipt), 500n);
  host.borrow(receipt, 500n);
  assert.throws(
    () => host.borrow(receipt, 1n),
    (e) => e instanceof LendingError && e.code === "COLLATERAL_RATIO",
  );
});

test("现货待审回调：交易内先操纵再取价则回执被抬高（脆弱性展示）", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const host = makeHost(pool, spotPendingFeed(pool));
  pool.swap(1_000_000n, "oneForZero", 100n); // 先推高价格
  const receipt = host.beginTx(100n); // 回执读到被操纵的现货
  host.deposit(1_000n);
  // 诚实价格下上限为 500；操纵后现货约 4 倍，上限随之抬高
  assert.ok(host.maxBorrow(receipt) > 1_500n, "现货回调把操纵价当作有效价格");
});

test("最大观察年龄：过期数据拒绝", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 0n, 1n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  const host = makeHost(pool, twapFeed(oracle, 10n), {
    maxObservationAge: 50n,
  });
  assert.throws(
    () => host.beginTx(1_000n),
    (e) => e instanceof LendingError && e.code === "STALE_OBSERVATION",
  );
});

test("最小流动性：低于阈值拒绝", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 0n, 1n);
  const host = makeHost(pool, spotPendingFeed(pool), {
    minLiquidity: 2_000_000n,
  });
  assert.throws(
    () => host.beginTx(0n),
    (e) => e instanceof LendingError && e.code === "INSUFFICIENT_LIQUIDITY",
  );
});

test("TWAP 回执：同交易交换不改变可借上限", () => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  const host = makeHost(pool, twapFeed(oracle, 100n));
  // 攻击者同时间大额交换推高现货
  pool.swap(1_000_000n, "oneForZero", 1_000n);
  const receipt = host.beginTx(1_000n);
  host.deposit(10_000n);
  // TWAP 仍为 1：上限 = 10000 * 1 * 1/2 = 5000
  assert.equal(host.maxBorrow(receipt), 5_000n);
});
