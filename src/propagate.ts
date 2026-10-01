// Uncertainty propagation.
//
// Linear models y = A x + b are exact: output covariance A Sigma A^T is
// computed with exact rationals and reported together with the exact
// coefficient matrix and a symbolic variance expression.
//
// Nonlinear models are only propagated as an explicitly declared
// first-order Taylor approximation (user-supplied derivatives at the
// operating point); the report states the approximation scope.

import {
  DEFAULT_ROUNDING,
  DEFAULT_STD_DECIMALS,
  Rational,
  sqrtRational,
  type RationalInput,
  type RoundingMode,
} from './rational.ts';
import {
  checkPSD,
  isSymmetric,
  matMul,
  matVec,
  toMatrix,
  transpose,
  type Matrix,
  type PsdFailure,
} from './matrix.ts';
import { buildSourceModel, type InputDecl, type SourceDecl } from './sources.ts';

export class NonPSDError extends Error {
  readonly failure: PsdFailure;
  constructor(failure: PsdFailure) {
    super(
      `Covariance matrix is not positive semidefinite (${failure.reason}); ` +
        `negative direction v = [${failure.vector.map((x) => x.toString()).join(', ')}] ` +
        `gives v^T Sigma v = ${failure.quadraticForm.toString()} < 0`,
    );
    this.name = 'NonPSDError';
    this.failure = failure;
  }
}

export interface LinearOutputSpec {
  id: string;
  // Exact coefficients of the inputs, in the same order as `inputs`.
  coefficients: RationalInput[];
  offset?: RationalInput;
}

export interface NonlinearOutputSpec {
  id: string;
  // f evaluated at the operating point (supplied by the caller).
  valueAtPoint: RationalInput;
  // First-order partial derivatives at the operating point, in input order.
  derivatives: RationalInput[];
}

interface BaseSpec {
  inputs: InputDecl[];
  // Exactly one of these two must be given.
  covariance?: RationalInput[][];
  sources?: SourceDecl[];
  stdDecimals?: number; // decimal places for reported std, default 6
  rounding?: RoundingMode; // rounding rule for std, default 'half-even'
}

export interface LinearSpec extends BaseSpec {
  outputs: LinearOutputSpec[];
}

export interface NonlinearSpec extends BaseSpec {
  outputs: NonlinearOutputSpec[];
  // Must be true: first-order Taylor approximation explicitly declared.
  firstOrderDeclared: boolean;
  // Optional free-text scope of validity, echoed into the report.
  validityNote?: string;
}

export interface StdReport {
  decimal: string;
  places: number;
  rounding: RoundingMode;
}

export interface OutputReport {
  id: string;
  value: Rational;
  variance: Rational;
  varianceExpression: string;
  std: StdReport;
}

export interface PropagationReport {
  kind: 'linear-exact' | 'nonlinear-first-order';
  assumptions: string[];
  warnings: string[];
  coefficients: { output: string; terms: { input: string; coefficient: Rational }[] }[];
  inputCovariance: Matrix;
  covariance: Matrix; // output covariance
  outputs: OutputReport[];
}

// Validate a directly supplied covariance matrix. Returns the PSD check
// result; throws nothing itself.
export function validateCovariance(matrix: RationalInput[][]): ReturnType<typeof checkPSD> {
  const S = toMatrix(matrix);
  if (!isSymmetric(S)) throw new Error('Covariance matrix must be symmetric');
  return checkPSD(S);
}

function resolveInputs(spec: BaseSpec): {
  inputIds: string[];
  values: Rational[];
  inputCovariance: Matrix;
  warnings: string[];
  assumptions: string[];
} {
  const inputIds = spec.inputs.map((x) => x.id);
  if (new Set(inputIds).size !== inputIds.length) {
    throw new Error(`Duplicate input id in: ${inputIds.join(', ')}`);
  }
  if ((spec.covariance === undefined) === (spec.sources === undefined)) {
    throw new Error('Provide exactly one of `covariance` or `sources`');
  }
  if (spec.sources !== undefined) {
    const model = buildSourceModel(spec.inputs, spec.sources);
    return {
      inputIds,
      values: model.values,
      inputCovariance: model.covariance,
      warnings: model.warnings,
      assumptions: [
        '相关输入共享底层误差源；输入协方差由源模型 Sigma = G D G^T 精确构造，每个独立源只计入一次',
      ],
    };
  }
  const S = toMatrix(spec.covariance as RationalInput[][]);
  if (S.length !== inputIds.length) {
    throw new Error(
      `Covariance dimension ${S.length} does not match ${inputIds.length} inputs`,
    );
  }
  if (!isSymmetric(S)) throw new Error('Covariance matrix must be symmetric');
  const psd = checkPSD(S);
  if (!psd.psd) throw new NonPSDError(psd.failure);
  return {
    inputIds,
    values: spec.inputs.map((x) => Rational.of(x.value)),
    inputCovariance: S,
    warnings: [],
    assumptions: ['输入协方差矩阵按给定使用，已通过精确半正定检验'],
  };
}

function varianceExpression(
  outputId: string,
  coeffs: Rational[],
  inputIds: string[],
  cov: Matrix,
  variance: Rational,
): string {
  const terms: string[] = [];
  for (let i = 0; i < coeffs.length; i++) {
    if (coeffs[i].isZero() || cov[i][i].isZero()) continue; // zero-variance dims do not contribute
    terms.push(`(${coeffs[i].toString()})^2*Var(${inputIds[i]})`);
  }
  for (let i = 0; i < coeffs.length; i++) {
    if (coeffs[i].isZero()) continue;
    for (let j = i + 1; j < coeffs.length; j++) {
      if (coeffs[j].isZero() || cov[i][j].isZero()) continue;
      terms.push(`2*(${coeffs[i].toString()})(${coeffs[j].toString()})*Cov(${inputIds[i]},${inputIds[j]})`);
    }
  }
  const body = terms.length === 0 ? '0' : terms.join(' + ');
  return `Var(${outputId}) = ${body} = ${variance.toString()}`;
}

function run(
  spec: BaseSpec,
  rows: { id: string; coeffs: Rational[]; offset: Rational }[],
  kind: PropagationReport['kind'],
  extraAssumptions: string[],
): PropagationReport {
  const { inputIds, values, inputCovariance, warnings, assumptions } = resolveInputs(spec);
  const n = inputIds.length;
  for (const row of rows) {
    if (row.coeffs.length !== n) {
      throw new Error(
        `Output '${row.id}': ${row.coeffs.length} coefficients for ${n} inputs`,
      );
    }
  }
  const A: Matrix = rows.map((r) => r.coeffs);
  const outputValues = matVec(A, values).map((v, i) => v.add(rows[i].offset));
  const covariance = matMul(matMul(A, inputCovariance), transpose(A));

  const places = spec.stdDecimals ?? DEFAULT_STD_DECIMALS;
  const rounding = spec.rounding ?? DEFAULT_ROUNDING;
  const outputs: OutputReport[] = rows.map((row, i) => {
    const variance = covariance[i][i];
    return {
      id: row.id,
      value: outputValues[i],
      variance,
      varianceExpression: varianceExpression(row.id, row.coeffs, inputIds, inputCovariance, variance),
      std: {
        decimal: sqrtRational(variance, places, rounding).toDecimal(places, rounding),
        places,
        rounding,
      },
    };
  });

  return {
    kind,
    assumptions: [
      ...assumptions,
      ...extraAssumptions,
      `标准不确定度由精确方差开方得到，按 '${rounding}' 规则舍入到 ${places} 位小数；方差与协方差本身保持精确有理数`,
    ],
    warnings,
    coefficients: rows.map((row) => ({
      output: row.id,
      terms: row.coeffs.map((c, i) => ({ input: inputIds[i], coefficient: c })),
    })),
    inputCovariance,
    covariance,
    outputs,
  };
}

// Exact linear propagation y = A x + b.
export function propagateLinear(spec: LinearSpec): PropagationReport {
  const rows = spec.outputs.map((o) => ({
    id: o.id,
    coeffs: o.coefficients.map((c) => Rational.of(c)),
    offset: Rational.of(o.offset ?? 0),
  }));
  return run(spec, rows, 'linear-exact', [
    '模型为精确线性 y = A x + b，系数为精确有理数，传播不含任何近似',
  ]);
}

// First-order propagation for nonlinear models. Refuses to run unless the
// caller explicitly declares the first-order approximation.
export function propagateFirstOrder(spec: NonlinearSpec): PropagationReport {
  if (!spec.firstOrderDeclared) {
    throw new Error(
      'Nonlinear model: first-order Taylor approximation must be explicitly declared ' +
        '(set firstOrderDeclared: true); exact propagation is not available for nonlinear models',
    );
  }
  const rows = spec.outputs.map((o) => ({
    id: o.id,
    coeffs: o.derivatives.map((d) => Rational.of(d)),
    offset: Rational.zero,
  }));
  const report = run(spec, rows, 'nonlinear-first-order', [
    '一阶泰勒近似：不确定度仅按工作点处偏导数传播，仅在声明的线性化范围内有效',
    ...(spec.validityNote ? [`声明的适用范围：${spec.validityNote}`] : []),
  ]);
  // Output values come from the caller-supplied f at the operating point,
  // not from the linearized model.
  report.outputs.forEach((out, i) => {
    out.value = Rational.of(spec.outputs[i].valueAtPoint);
  });
  return report;
}
