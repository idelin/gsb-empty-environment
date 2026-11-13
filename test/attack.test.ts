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
import { findAttackWitness, runAttackScenario } from "../src/attack.ts";
import type { AttackSearchOptions, MarginSystem } from "../src/attack.ts";
import { Rat } from "../src/rational.ts";

const NOW = 1_000n;
const COLLATERAL = 10_000n;
const TOLERANCE = 50_000n;

const baseOpts = (
  makeSystem: () => MarginSystem,
  overrides: Partial<AttackSearchOptions> = {},
): AttackSearchOptions => ({
  makeSystem,
  now: NOW,
  collateral: COLLATERAL,
  reserveTolerance: TOLERANCE,
  maxActions: 3,
  ...overrides,
});

/** 脆弱宿主：现货待审回调直接读实时储备。 */
const makeSpotSystem = (): MarginSystem => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const host = new LendingHost({
    pool,
    feed: spotPendingFeed(pool),
    collateralFactor: Rat.of(4n, 5n),
    maxObservationAge: 10_000n,
    minLiquidity: 1n,
  });
  return { pool, host };
};

/** 防护宿主：窗口 W=100 的 TWAP 回执。 */
const makeTwapSystem = (maxObservationAge = 10_000n): MarginSystem => {
  const pool = new ConstantProductPool(1_000_000n, 1_000_000n, 3n, 1000n);
  const oracle = new TwapOracle(pool);
  oracle.update(0n);
  const host = new LendingHost({
    pool,
    feed: twapFeed(oracle, 100n),
    collateralFactor: Rat.of(4n, 5n),
    maxObservationAge,
    minLiquidity: 1n,
  });
  return { pool, host };
};

test("现货宿主：找到至多 3 笔、储备恢复且坏账为正的最短见证", () => {
  const opts = baseOpts(makeSpotSystem);
  const witness = findAttackWitness(opts);
  assert.ok(witness, "现货回调下应存在攻击见证");
  assert.equal(witness.actions.length, 3); // 借入交换 → 借款 → 反向交换
  assert.equal(witness.actions[0]!.kind, "swap");
  assert.equal(witness.actions[1]!.kind, "borrow");
  assert.equal(witness.actions[2]!.kind, "swap");
  assert.ok(witness.badDebt.isPositive(), "坏账为正");
  assert.ok(
    witness.reserveDrift.dx <= TOLERANCE &&
      witness.reserveDrift.dx >= -TOLERANCE &&
      witness.reserveDrift.dy <= TOLERANCE &&
      witness.reserveDrift.dy >= -TOLERANCE,
    "储备在容差内恢复",
  );
  // 重放见证，结论一致
  const replay = runAttackScenario(opts, witness.actions);
  assert.ok(replay.ok);
  assert.ok(replay.badDebt.isPositive());
});

test("TWAP 宿主（窗口 W）：同交易操纵被限制，搜索无见证", () => {
  const opts = baseOpts(() => makeTwapSystem());
  const witness = findAttackWitness(opts);
  assert.equal(witness, null, "TWAP 限制借款，坏账不为正");
  // 直接验证：攻击交换后可借上限仍锚定真实价格
  const sys = makeTwapSystem();
  sys.pool.swap(1_000_000n, "oneForZero", NOW);
  const receipt = sys.host.beginTx(NOW);
  sys.host.deposit(COLLATERAL);
  assert.equal(sys.host.maxBorrow(receipt), 8_000n); // 10000 * 1 * 4/5
});

test("TWAP 宿主（观察过期）：直接拒绝借款", () => {
  const opts = baseOpts(() => makeTwapSystem(50n)); // maxAge 远小于 now
  const witness = findAttackWitness(opts);
  assert.equal(witness, null);
  const sys = makeTwapSystem(50n);
  assert.throws(
    () => sys.host.beginTx(NOW),
    (e) => e instanceof LendingError && e.code === "STALE_OBSERVATION",
  );
});

test("动作数上限为 2 时找不到需要 3 笔的见证", () => {
  const opts = baseOpts(makeSpotSystem, { maxActions: 2 });
  assert.equal(findAttackWitness(opts), null);
});
