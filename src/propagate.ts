/**
 * 不确定度传播。
 *
 * - propagateLinear：线性模型 y = Σ aᵢxᵢ (+ const)，基于误差源协方差图。
 *   Var(y) = aᵀΣa 为精确恒等式，结果给精确方差表达式。
 * - propagateLinearWithCovariance：用户直接给定协方差矩阵。
 *   先做对称性与精确 LDLᵀ 半正定检验；非半正定一律拒绝并附负方向证据。
 * - propagateFirstOrder：非线性模型，仅输出一阶泰勒近似，
 *   并在假设中显式声明其适用范围。
 *
 * 零方差维度与零系数维度不贡献方差，在结果中显式列出。
 */

import { Decimal, DEFAULT_ROUNDING_MODE, DEFAULT_SIG_DIGITS, type RoundingMode } from './decimal.ts';
import { Rational } from './rational.ts';
import {
  checkPSD,
  checkSymmetric,
  matFromDecimals,
  quadraticForm,
  vecFromDecimals,
  type RVec,
} from './matrix.ts';
import { ModelError, type CovarianceGraph } from './model.ts';

export interface RoundingSpec {
  sigDigits: number;
  mode: RoundingMode;
}

export const DEFAULT_ROUNDING_SPEC: RoundingSpec = {
  sigDigits: DEFAULT_SIG_DIGITS,
  mode: DEFAULT_ROUNDING_MODE,
};

export interface LinearTerm {
  input: string;
  coeff: Decimal;
}

export interface NonContributing {
  input: string;
  reason: string;
}

export interface PropagationResult {
  kind: 'linear-exact' | 'first-order-approximation';
  modelExpr: string;
  /** 输出估计值（精确）。 */
  value: Decimal;
  /** 输出方差（精确有理数）。 */
  variance: Rational;
  /** 精确方差表达式（源层面或协方差层面展开）。 */
  varianceExpr: string;
  /** 标准不确定度 u(y)=√Var(y)，按 rounding 规则舍入（唯一舍入点）。 */
  std: Decimal;
  /** 精确线性系数（传播所用）。 */
  coefficients: Array<{ input: string; coeff: Decimal }>;
  nonContributing: NonContributing[];
  assumptions: string[];
  warnings: string[];
  rounding: RoundingSpec;
}

/** 协方差矩阵非半正定时抛出，附负方向证据。 */
export class NonPSDError extends Error {
  /** 证据向量（精确有理数）。 */
  readonly witnessExact: RVec;
  /** 证据向量（12 位有效数字 half-even 显示值）。 */
  readonly witness: Decimal[];
  /** vᵀΣv 的精确值（< 0）。 */
  readonly quadraticValue: Rational;
  constructor(witness: RVec, value: Rational) {
    super(
      `协方差矩阵非半正定，拒绝传播。负方向证据：存在 v 使 vᵀΣv = ${value.toString()} < 0`,
    );
    this.name = 'NonPSDError';
    this.witnessExact = witness;
    this.witness = witness.map((r) => r.toDecimal(12, 'half-even'));
    this.quadraticValue = value;
  }
}

interface LabeledCoeff {
  input: string;
  coeff: Decimal;
  index: number;
}

/** 把 (系数, 标签) 列表拼成 'a·x1 - b·x2 + c' 形式；标签为空串表示常数项。 */
function joinTerms(terms: Array<{ coeff: Decimal; label: string }>): string {
  let out = '';
  for (const t of terms) {
    const neg = t.coeff.sign < 0;
    const mag = neg ? t.coeff.neg().toString() : t.coeff.toString();
    const body = t.label === '' ? mag : `${mag}·${t.label}`;
    if (out === '') out = neg ? `-${body}` : body;
    else out += neg ? ` - ${body}` : ` + ${body}`;
  }
  return out === '' ? '0' : out;
}

function finishLinear(
  kind: PropagationResult['kind'],
  modelExpr: string,
  value: Decimal,
  labeled: LabeledCoeff[],
  graph: CovarianceGraph,
  rounding: RoundingSpec,
  extraAssumptions: string[],
  zeroReason: (t: LabeledCoeff) => string | null,
): PropagationResult {
  const n = graph.inputs.length;
  const a: RVec = Array.from({ length: n }, () => Rational.zero());
  for (const t of labeled) a[t.index] = Rational.from(t.coeff);

  const variance = quadraticForm(a, graph.covariance);

  // 源层面精确展开：Var(y) = Σ_s (Σᵢ aᵢcᵢs)² · u(s)²
  const exprTerms: string[] = [];
  for (const s of graph.sources) {
    let k = Rational.zero();
    for (const t of labeled) {
      const c = graph.sensitivities[t.index]!.get(s.id);
      if (c !== undefined) k = k.add(c.mul(Rational.from(t.coeff)));
    }
    if (!k.isZero()) {
      const kd = k.toDecimalExact();
      exprTerms.push(`(${kd ? kd.toString() : k.toString()})^2·u(${s.id})^2`);
    }
  }
  const varianceExpr = exprTerms.length > 0 ? exprTerms.join(' + ') : '0';

  const nonContributing: NonContributing[] = [];
  for (const t of labeled) {
    const reason = zeroReason(t);
    if (reason !== null) nonContributing.push({ input: t.input, reason });
    else if (graph.inputVariances[t.index]!.isZero()) {
      nonContributing.push({ input: t.input, reason: '零方差维度，不贡献' });
    }
  }

  const std = variance.sqrt(rounding.sigDigits, rounding.mode);

  const assumptions = [
    '各误差源相互独立；同一误差源被多个输入引用时按协方差图计入且仅计入一次（不双计）',
    '全部中间计算为 BigInt 精确有理数，未经 Number 中转',
    `唯一舍入点：u(y)=√Var(y) 按 ${rounding.mode} 舍入到 ${rounding.sigDigits} 位有效数字`,
    ...extraAssumptions,
  ];

  return {
    kind,
    modelExpr,
    value,
    variance,
    varianceExpr,
    std,
    coefficients: labeled.map((t) => ({ input: t.input, coeff: t.coeff })),
    nonContributing,
    assumptions,
    warnings: graph.warnings.slice(),
    rounding,
  };
}

function resolveTerms(terms: LinearTerm[], graph: CovarianceGraph, what: string): LabeledCoeff[] {
  if (terms.length === 0) throw new ModelError(`${what}至少需要一个项`);
  const index = new Map(graph.inputs.map((inp, i) => [inp.id, i] as const));
  const seen = new Set<string>();
  const labeled: LabeledCoeff[] = [];
  for (const t of terms) {
    const i = index.get(t.input);
    if (i === undefined) throw new ModelError(`${what}引用了未知输入: ${t.input}`);
    if (seen.has(t.input)) throw new ModelError(`${what}中输入重复: ${t.input}`);
    seen.add(t.input);
    labeled.push({ input: t.input, coeff: t.coeff, index: i });
  }
  return labeled;
}

/** 线性模型 y = Σ aᵢxᵢ (+ constant)，基于误差源协方差图。精确传播。 */
export function propagateLinear(
  terms: LinearTerm[],
  graph: CovarianceGraph,
  options: { constant?: Decimal; rounding?: RoundingSpec } = {},
): PropagationResult {
  const rounding = options.rounding ?? DEFAULT_ROUNDING_SPEC;
  const labeled = resolveTerms(terms, graph, '线性模型');
  let value = options.constant ?? Decimal.zero();
  for (const t of labeled) value = value.add(t.coeff.mul(graph.inputs[t.index]!.value));
  const termList = labeled.map((t) => ({ coeff: t.coeff, label: t.input }));
  if (options.constant !== undefined) termList.push({ coeff: options.constant, label: '' });
  const modelExpr = `y = ${joinTerms(termList)}`;
  return finishLinear(
    'linear-exact',
    modelExpr,
    value,
    labeled,
    graph,
    rounding,
    ['线性模型：Var(y)=aᵀΣa 为精确恒等式（非近似）'],
    (t) => (t.coeff.isZero() ? '线性系数为零，不贡献' : null),
  );
}

/** 非线性模型：仅一阶泰勒近似。偏导数由调用方在标称点处提供。 */
export function propagateFirstOrder(
  nominal: Decimal,
  modelExpr: string,
  partials: Array<{ input: string; deriv: Decimal }>,
  graph: CovarianceGraph,
  options: { rounding?: RoundingSpec } = {},
): PropagationResult {
  const rounding = options.rounding ?? DEFAULT_ROUNDING_SPEC;
  const labeled = resolveTerms(
    partials.map((p) => ({ input: p.input, coeff: p.deriv })),
    graph,
    '一阶传播',
  );
  return finishLinear(
    'first-order-approximation',
    modelExpr,
    nominal,
    labeled,
    graph,
    rounding,
    [
      '非线性模型：本结果仅为一阶泰勒近似 Var(y)≈JᵀΣJ，高阶项未计入；仅在标称点附近线性近似成立的范围内有效',
      '偏导数（精确线性系数）由调用方在标称点处给出，本库不做数值微分',
    ],
    (t) => (t.coeff.isZero() ? '偏导数为零，该维度不贡献' : null),
  );
}

/** 用户直接给定协方差矩阵的线性传播。非半正定一律拒绝并附负方向证据。 */
export function propagateLinearWithCovariance(
  coeffs: Decimal[],
  values: Decimal[],
  cov: Decimal[][],
  options: { labels?: string[]; rounding?: RoundingSpec } = {},
): PropagationResult {
  const rounding = options.rounding ?? DEFAULT_ROUNDING_SPEC;
  const n = coeffs.length;
  if (n === 0) throw new ModelError('至少需要一个输入');
  if (values.length !== n || cov.length !== n || cov.some((row) => row.length !== n)) {
    throw new ModelError('系数、值与协方差矩阵维度不一致');
  }
  const labels = options.labels ?? coeffs.map((_, i) => `x${i + 1}`);
  if (labels.length !== n) throw new ModelError('labels 长度与维度不一致');

  const m = matFromDecimals(cov);
  if (!checkSymmetric(m)) {
    throw new ModelError('协方差矩阵不对称：Cov(i,j) 必须等于 Cov(j,i)');
  }
  const psd = checkPSD(m);
  if (!psd.ok) throw new NonPSDError(psd.witness, psd.value);

  const a = vecFromDecimals(coeffs);
  const variance = quadraticForm(a, m);

  let value = Decimal.zero();
  for (let i = 0; i < n; i++) value = value.add(coeffs[i]!.mul(values[i]!));

  // 协方差层面精确展开：Σ aᵢ²Var(xᵢ) + 2Σ aᵢaⱼCov(xᵢ,xⱼ)
  const exprTerms: string[] = [];
  for (let i = 0; i < n; i++) {
    if (!cov[i]![i]!.isZero() && !coeffs[i]!.isZero()) {
      exprTerms.push(`(${coeffs[i]!.toString()})^2·Var(${labels[i]!})`);
    }
  }
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (!cov[i]![j]!.isZero() && !coeffs[i]!.isZero() && !coeffs[j]!.isZero()) {
        exprTerms.push(
          `2·(${coeffs[i]!.toString()})·(${coeffs[j]!.toString()})·Cov(${labels[i]!},${labels[j]!})`,
        );
      }
    }
  }
  const varianceExpr = exprTerms.length > 0 ? exprTerms.join(' + ') : '0';

  const nonContributing: NonContributing[] = [];
  for (let i = 0; i < n; i++) {
    if (coeffs[i]!.isZero()) nonContributing.push({ input: labels[i]!, reason: '线性系数为零，不贡献' });
    else if (cov[i]![i]!.isZero()) nonContributing.push({ input: labels[i]!, reason: '零方差维度，不贡献' });
  }

  const std = variance.sqrt(rounding.sigDigits, rounding.mode);

  const modelExpr = `y = ${joinTerms(coeffs.map((c, i) => ({ coeff: c, label: labels[i]! })))}`;

  return {
    kind: 'linear-exact',
    modelExpr,
    value,
    variance,
    varianceExpr,
    std,
    coefficients: coeffs.map((c, i) => ({ input: labels[i]!, coeff: c })),
    nonContributing,
    assumptions: [
      '协方差矩阵由用户直接给定，已通过精确 LDLᵀ 半正定检验（含零方差维度情形）',
      '线性模型：Var(y)=aᵀΣa 为精确恒等式（非近似）',
      '全部中间计算为 BigInt 精确有理数，未经 Number 中转',
      `唯一舍入点：u(y)=√Var(y) 按 ${rounding.mode} 舍入到 ${rounding.sigDigits} 位有效数字`,
    ],
    warnings: [],
    rounding,
  };
}
