export type SwapDirection = "zeroForOne" | "oneForZero";

/**
 * 常数乘积池。储备 x,y 为正整数；输入先扣除有理手续费，
 * 在 x*y 不减规则下求最大整数输出。
 */
export class ConstantProductPool {
  private observer: ((time: bigint) => void) | null = null;
  x: bigint;
  y: bigint;
  readonly feeNum: bigint;
  readonly feeDen: bigint;

  constructor(x: bigint, y: bigint, feeNum: bigint, feeDen: bigint) {
    if (x <= 0n || y <= 0n) throw new RangeError("reserves must be positive");
    if (feeDen <= 0n || feeNum < 0n || feeNum >= feeDen) {
      throw new RangeError("invalid fee fraction");
    }
    this.x = x;
    this.y = y;
    this.feeNum = feeNum;
    this.feeDen = feeDen;
  }

  /** 每次储备变化前触发（预言机在此落观察）。 */
  setObserver(observer: ((time: bigint) => void) | null): void {
    this.observer = observer;
  }

  invariant(): bigint {
    return this.x * this.y;
  }

  /** 最大整数输出：floor(reserveOut * effIn / (reserveIn + effIn))，effIn 为扣费后输入。 */
  getAmountOut(amountIn: bigint, dir: SwapDirection): bigint {
    if (amountIn <= 0n) throw new RangeError("amountIn must be positive");
    const [reserveIn, reserveOut] =
      dir === "zeroForOne" ? [this.x, this.y] : [this.y, this.x];
    const effNum = amountIn * (this.feeDen - this.feeNum);
    const num = reserveOut * effNum;
    const den = reserveIn * this.feeDen + effNum;
    const out = num / den;
    if (out <= 0n) throw new RangeError("insufficient output amount");
    if (out >= reserveOut) throw new RangeError("output would drain reserves");
    return out;
  }

  /** 先在旧储备上落观察，再按最大整数输出改储备。 */
  swap(amountIn: bigint, dir: SwapDirection, time: bigint): bigint {
    this.observer?.(time);
    const out = this.getAmountOut(amountIn, dir);
    if (dir === "zeroForOne") {
      this.x += amountIn;
      this.y -= out;
    } else {
      this.y += amountIn;
      this.x -= out;
    }
    return out;
  }
}
