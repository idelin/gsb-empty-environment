import type { RationalCoefficients } from './types.ts';

/**
 * 滤波器状态：直接 II 型所需的历史输入/输出。
 * xHist[0] = x[n]，xHist[1] = x[n−1]，…；yHist 同理。
 */
export interface FilterState {
  xHist: number[];
  yHist: number[];
}

/**
 * 有理（ARMA/IIR）滤波器内核：
 *   y[n] = ( Σᵢ bᵢ·x[n−i] − Σⱼ≥₁ aⱼ·y[n−j] ) / a₀
 * 构造时以 a₀ 归一化。状态可快照/克隆，是局部重算的基础。
 */
export class RationalFilter {
  readonly b: number[];
  readonly a: number[];

  constructor(coeffs: RationalCoefficients) {
    const { b, a } = coeffs;
    if (b.length === 0) throw new Error('分子系数 b 不能为空');
    if (a.length === 0) throw new Error('分母系数 a 不能为空');
    if (a[0] === 0) throw new Error('分母系数 a₀ 不能为 0');
    const a0 = a[0];
    this.b = b.map((v) => v / a0);
    this.a = a.map((v) => v / a0);
  }

  /** 零初始状态（段首 / 长缺口断开后使用） */
  zeroState(): FilterState {
    return {
      xHist: new Array(this.b.length).fill(0),
      yHist: new Array(Math.max(this.a.length - 1, 0)).fill(0),
    };
  }

  /** 就地推进状态并返回 y[n] */
  step(state: FilterState, x: number): number {
    const { b, a } = this;
    state.xHist.unshift(x);
    state.xHist.length = b.length;
    let y = 0;
    for (let i = 0; i < b.length; i++) y += b[i] * state.xHist[i];
    for (let j = 1; j < a.length; j++) y -= a[j] * state.yHist[j - 1];
    state.yHist.unshift(y);
    state.yHist.length = Math.max(a.length - 1, 0);
    return y;
  }
}

export function cloneState(state: FilterState): FilterState {
  return { xHist: state.xHist.slice(), yHist: state.yHist.slice() };
}
