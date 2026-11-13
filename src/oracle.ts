import { Rat } from "./rational.ts";
import type { ConstantProductPool } from "./amm.ts";

export const ORACLE_CAPACITY = 64;

export type OracleErrorCode =
  | "INSUFFICIENT_HISTORY"
  | "CLOCK_REGRESSION"
  | "INVALID_WINDOW"
  | "ZERO_PRICE_DENOMINATOR";

export class OracleError extends Error {
  readonly code: OracleErrorCode;

  constructor(code: OracleErrorCode, message: string) {
    super(message);
    this.name = "OracleError";
    this.code = code;
  }
}

export interface Observation {
  time: bigint;
  cumulative0: Rat; // price0 = y/x 的时间积分
  cumulative1: Rat; // price1 = x/y 的时间积分
}

export interface ConsultResult {
  price0: Rat;
  price1: Rat;
  duration: bigint;
  from: Observation;
}

/**
 * 时间加权预言机：每次储备变化前，按单调时间差把精确有理价格
 * 累加进累计值，并写入最多 64 条环形观察。
 *
 * 注意：TWAP 只是抬高操纵成本（操纵须跨时间维持），并非完全抗操纵。
 */
export class TwapOracle {
  private readonly ring: (Observation | undefined)[];
  private readonly pool: ConstantProductPool;
  readonly capacity: number;
  private writeIndex = 0;
  private size = 0;

  constructor(pool: ConstantProductPool, capacity: number = ORACLE_CAPACITY) {
    if (!Number.isInteger(capacity) || capacity < 1) {
      throw new RangeError("capacity must be a positive integer");
    }
    this.pool = pool;
    this.capacity = capacity;
    this.ring = new Array<Observation | undefined>(capacity);
    pool.setObserver((time) => this.update(time));
  }

  get length(): number {
    return this.size;
  }

  latest(): Observation | undefined {
    if (this.size === 0) return undefined;
    return this.ring[(this.writeIndex - 1 + this.capacity) % this.capacity];
  }

  /** 按时间升序返回当前环内观察。 */
  observations(): Observation[] {
    const out: Observation[] = [];
    const start = this.size < this.capacity ? 0 : this.writeIndex;
    for (let i = 0; i < this.size; i++) {
      out.push(this.ring[(start + i) % this.capacity]!);
    }
    return out;
  }

  private spot(): { p0: Rat; p1: Rat } {
    if (this.pool.x === 0n || this.pool.y === 0n) {
      throw new OracleError(
        "ZERO_PRICE_DENOMINATOR",
        "pool reserve is zero, price denominator undefined",
      );
    }
    return {
      p0: Rat.of(this.pool.y, this.pool.x),
      p1: Rat.of(this.pool.x, this.pool.y),
    };
  }

  /** 在储备变化前调用：用变化前价格按时间差累加，然后写入一条观察。 */
  update(time: bigint): void {
    const { p0, p1 } = this.spot();
    const prev = this.latest();
    let cumulative0: Rat;
    let cumulative1: Rat;
    if (prev === undefined) {
      cumulative0 = Rat.zero();
      cumulative1 = Rat.zero();
    } else {
      if (time < prev.time) {
        throw new OracleError(
          "CLOCK_REGRESSION",
          `time ${time} is before latest observation at ${prev.time}`,
        );
      }
      const dt = time - prev.time;
      cumulative0 = prev.cumulative0.add(p0.scale(dt));
      cumulative1 = prev.cumulative1.add(p1.scale(dt));
    }
    this.ring[this.writeIndex] = { time, cumulative0, cumulative1 };
    this.writeIndex = (this.writeIndex + 1) % this.capacity;
    if (this.size < this.capacity) this.size++;
  }

  /**
   * 窗口 W 查询：找 time <= now-W 的最近观察，用该观察到 now 的
   * 累计差除以实际时长。历史不足、时钟倒退、时长小于 W、价格分母为 0 均拒绝。
   */
  consult(now: bigint, window: bigint): ConsultResult {
    if (window <= 0n) {
      throw new OracleError("INVALID_WINDOW", "window must be positive");
    }
    const latest = this.latest();
    if (latest === undefined) {
      throw new OracleError("INSUFFICIENT_HISTORY", "no observations yet");
    }
    if (now < latest.time) {
      throw new OracleError(
        "CLOCK_REGRESSION",
        `now ${now} is before latest observation at ${latest.time}`,
      );
    }
    const { p0, p1 } = this.spot();
    const dt = now - latest.time;
    const cum0 = latest.cumulative0.add(p0.scale(dt));
    const cum1 = latest.cumulative1.add(p1.scale(dt));

    const target = now - window;
    let chosen: Observation | undefined;
    for (const obs of this.observations()) {
      if (obs.time <= target) chosen = obs;
      else break;
    }
    if (chosen === undefined) {
      throw new OracleError(
        "INSUFFICIENT_HISTORY",
        `no observation at or before ${target}`,
      );
    }
    const duration = now - chosen.time;
    if (duration < window) {
      throw new OracleError(
        "INVALID_WINDOW",
        `duration ${duration} shorter than window ${window}`,
      );
    }
    const dur = Rat.of(duration);
    return {
      price0: cum0.sub(chosen.cumulative0).div(dur),
      price1: cum1.sub(chosen.cumulative1).div(dur),
      duration,
      from: chosen,
    };
  }
}
