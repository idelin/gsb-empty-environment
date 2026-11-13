import { Rat } from "./rational.ts";
import type { ConstantProductPool } from "./amm.ts";
import type { TwapOracle } from "./oracle.ts";

/** 价格回执：同一交易内先固定，再据此检查抵押率。 */
export interface PriceReceipt {
  price: Rat; // token0 以 token1 计价
  asOf: bigint; // 价格数据锚定的观察时间（用于年龄检查）
}

export type PriceFeed = (now: bigint) => PriceReceipt;

export type LendingErrorCode =
  | "STALE_OBSERVATION"
  | "INSUFFICIENT_LIQUIDITY"
  | "COLLATERAL_RATIO"
  | "ZERO_PRICE_DENOMINATOR"
  | "INVALID_AMOUNT"
  | "INVALID_CONFIG";

export class LendingError extends Error {
  readonly code: LendingErrorCode;

  constructor(code: LendingErrorCode, message: string) {
    super(message);
    this.name = "LendingError";
    this.code = code;
  }
}

/**
 * 现货待审回调（脆弱模式）：直接读取池内实时储备，
 * 包含尚未落观察的临时状态，同交易大额交换即可操纵。
 */
export function spotPendingFeed(pool: ConstantProductPool): PriceFeed {
  return (now) => {
    if (pool.x === 0n || pool.y === 0n) {
      throw new LendingError(
        "ZERO_PRICE_DENOMINATOR",
        "pool reserve is zero, price denominator undefined",
      );
    }
    return { price: Rat.of(pool.y, pool.x), asOf: now };
  };
}

/** TWAP 回执：只读已落观察的累计值，与交换的临时状态隔离。 */
export function twapFeed(oracle: TwapOracle, window: bigint): PriceFeed {
  return (now) => {
    const result = oracle.consult(now, window);
    const latest = oracle.latest();
    return { price: result.price0, asOf: latest ? latest.time : now };
  };
}

export interface LendingOptions {
  pool: ConstantProductPool;
  feed: PriceFeed;
  collateralFactor: Rat; // <= 1
  maxObservationAge: bigint;
  minLiquidity: bigint;
}

/** 单仓位借贷宿主：抵押 token0，借出 token1。 */
export class LendingHost {
  readonly opts: LendingOptions;
  collateral = 0n;
  debt = 0n;

  constructor(opts: LendingOptions) {
    if (
      opts.collateralFactor.cmp(Rat.zero()) <= 0 ||
      opts.collateralFactor.cmp(Rat.of(1n)) > 0
    ) {
      throw new LendingError(
        "INVALID_CONFIG",
        "collateralFactor must be in (0, 1]",
      );
    }
    if (opts.maxObservationAge < 0n || opts.minLiquidity < 0n) {
      throw new LendingError(
        "INVALID_CONFIG",
        "maxObservationAge/minLiquidity must be non-negative",
      );
    }
    this.opts = opts;
  }

  /** 同一交易内先固定价格回执，并校验观察年龄与池流动性。 */
  beginTx(now: bigint): PriceReceipt {
    const receipt = this.opts.feed(now);
    if (receipt.asOf > now || now - receipt.asOf > this.opts.maxObservationAge) {
      throw new LendingError(
        "STALE_OBSERVATION",
        `price data anchored at ${receipt.asOf} is too old for now=${now}`,
      );
    }
    const { x, y } = this.opts.pool;
    const liquidity = x < y ? x : y;
    if (liquidity < this.opts.minLiquidity) {
      throw new LendingError(
        "INSUFFICIENT_LIQUIDITY",
        `pool liquidity ${liquidity} below minimum ${this.opts.minLiquidity}`,
      );
    }
    return receipt;
  }

  deposit(amount: bigint): void {
    if (amount <= 0n) {
      throw new LendingError("INVALID_AMOUNT", "deposit must be positive");
    }
    this.collateral += amount;
  }

  /** 以回执价格（而非实时价格）计算可借上限。 */
  maxBorrow(receipt: PriceReceipt): bigint {
    return receipt.price
      .scale(this.collateral)
      .mul(this.opts.collateralFactor)
      .floor();
  }

  borrow(receipt: PriceReceipt, amount: bigint): void {
    if (amount <= 0n) {
      throw new LendingError("INVALID_AMOUNT", "borrow must be positive");
    }
    if (this.debt + amount > this.maxBorrow(receipt)) {
      throw new LendingError(
        "COLLATERAL_RATIO",
        "borrow exceeds collateralized limit at receipt price",
      );
    }
    this.debt += amount;
  }
}
