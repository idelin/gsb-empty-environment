import { Interval, interval } from './interval';
import { IntervalProvider, Refinement } from './provider';
import { Rational } from './rational';

export type SimStep =
  | { readonly kind: 'interval'; readonly interval: Interval }
  | { readonly kind: 'exact'; readonly value: Rational }
  | { readonly kind: 'exhausted' };

export interface HalvingOptions {
  /** 初始半径 r：初始区间为 [v - r, v + r]。 */
  radius: Rational | number;
  /** 区间收紧次数（每次半径减半）。 */
  intervalSteps: number;
  /** 区间阶段结束后是否给出精确证书。 */
  emitExact?: boolean;
}

/**
 * 模拟提供者：按脚本逐步返回收紧区间 / 精确证书。
 * 脚本用完后返回 exhausted；被 cancel 后立即返回 exhausted。
 */
export class SimulatedProvider implements IntervalProvider {
  readonly id: string;
  private readonly steps: ReadonlyArray<SimStep>;
  private pos = 0;
  private cancelledFlag = false;
  /** 已被调用 refine 的次数（含初始区间）。 */
  refineCalls = 0;

  constructor(id: string, steps: ReadonlyArray<SimStep>) {
    if (steps.length === 0) throw new Error('SimulatedProvider: script must not be empty');
    this.id = id;
    this.steps = steps;
  }

  /**
   * 围绕真实值 v 生成几何收紧脚本：
   * 第 i 个区间为 [v - r/2^i, v + r/2^i]，严格嵌套且始终包含 v。
   */
  static halving(id: string, trueValue: Rational | number, opts: HalvingOptions): SimulatedProvider {
    const v = typeof trueValue === 'number' ? Rational.of(trueValue) : trueValue;
    const r = typeof opts.radius === 'number' ? Rational.of(opts.radius) : opts.radius;
    const steps: SimStep[] = [];
    for (let i = 0; i <= opts.intervalSteps; i++) {
      const shrink = Rational.of(r.num, r.den * 2n ** BigInt(i));
      steps.push({ kind: 'interval', interval: interval(v.sub(shrink), v.add(shrink)) });
    }
    if (opts.emitExact) steps.push({ kind: 'exact', value: v });
    return new SimulatedProvider(id, steps);
  }

  refine(): Refinement {
    this.refineCalls += 1;
    if (this.cancelledFlag) return { kind: 'exhausted' };
    if (this.pos >= this.steps.length) return { kind: 'exhausted' };
    const step = this.steps[this.pos];
    this.pos += 1;
    return step;
  }

  cancel(): void {
    this.cancelledFlag = true;
  }

  get cancelled(): boolean {
    return this.cancelledFlag;
  }
}
