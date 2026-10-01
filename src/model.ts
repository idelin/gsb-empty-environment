/**
 * 误差源协方差图。
 *
 * 模型：每个输入 xᵢ 的不确定度来自若干相互独立的底层误差源 s，
 *   u²(xᵢ) = Σ_s cᵢs² · u²(s)
 * 输入间协方差由共享源唯一确定：
 *   Cov(xᵢ, xⱼ) = Σ_s cᵢs · cⱼs · u²(s)
 * 因此同一物理误差源被多个输入引用时只计入一次（不双计）；
 * 完全相关的误差按代数叠加而非独立平方和。
 *
 * 图结构：源节点 --(灵敏度系数 cᵢs)--> 输入节点。
 * 校验：重复源 id、输入重复引用同一源（双计保护）、未知源引用、
 * 未使用的源（警告）。
 */

import { Decimal } from './decimal.ts';
import { Rational } from './rational.ts';
import { zeroMat, type RMat } from './matrix.ts';

export interface SourceDecl {
  id: string;
  std: Decimal; // u(s)，标准不确定度
}

export interface Contribution {
  source: string;
  coeff: Decimal; // 灵敏度系数 c：该源对输入的贡献为 c·u(s)
}

export interface InputDecl {
  id: string;
  value: Decimal;
  contributions: Contribution[];
}

export class ModelError extends Error {
  readonly details: string[];
  constructor(message: string, details: string[] = []) {
    super(message);
    this.name = 'ModelError';
    this.details = details;
  }
}

export interface CovarianceGraph {
  inputs: InputDecl[];
  sources: SourceDecl[];
  /** Σ：输入间协方差矩阵（精确有理数），按 inputs 顺序。 */
  covariance: RMat;
  /** 每个输入的合成方差 u²(xᵢ)。 */
  inputVariances: Rational[];
  /** 每个输入到各源的灵敏度系数表（sourceId → c）。 */
  sensitivities: Map<string, Rational>[];
  warnings: string[];
}

export function buildCovarianceGraph(sources: SourceDecl[], inputs: InputDecl[]): CovarianceGraph {
  const warnings: string[] = [];

  const sourceIds = new Set<string>();
  const dupSources = new Set<string>();
  for (const s of sources) {
    if (sourceIds.has(s.id)) dupSources.add(s.id);
    sourceIds.add(s.id);
  }
  if (dupSources.size > 0) {
    throw new ModelError(`误差源 id 重复声明: ${[...dupSources].join(', ')}`);
  }

  const inputIds = new Set<string>();
  const dupInputs = new Set<string>();
  for (const inp of inputs) {
    if (inputIds.has(inp.id)) dupInputs.add(inp.id);
    inputIds.add(inp.id);
  }
  if (dupInputs.size > 0) {
    throw new ModelError(`输入 id 重复声明: ${[...dupInputs].join(', ')}`);
  }

  const sourceMap = new Map(sources.map((s) => [s.id, s]));
  const usedSources = new Set<string>();

  for (const inp of inputs) {
    const seen = new Set<string>();
    const dup: string[] = [];
    for (const c of inp.contributions) {
      if (!sourceMap.has(c.source)) {
        throw new ModelError(`输入 ${inp.id} 引用了未声明的误差源: ${c.source}`);
      }
      if (seen.has(c.source)) dup.push(c.source);
      seen.add(c.source);
      usedSources.add(c.source);
    }
    if (dup.length > 0) {
      throw new ModelError(
        `输入 ${inp.id} 重复引用同一误差源（会把同一误差当作两个独立误差双计）: ${dup.join(', ')}`,
      );
    }
  }

  for (const s of sources) {
    if (!usedSources.has(s.id)) {
      warnings.push(`误差源 ${s.id} 已声明但未被任何输入引用，不参与传播`);
    }
  }

  const n = inputs.length;
  const sensitivities: Map<string, Rational>[] = inputs.map((inp) => {
    const m = new Map<string, Rational>();
    for (const c of inp.contributions) m.set(c.source, Rational.from(c.coeff));
    return m;
  });

  const covariance = zeroMat(n);
  for (const s of sources) {
    const u = Rational.from(s.std);
    const u2 = u.mul(u);
    for (let i = 0; i < n; i++) {
      const ci = sensitivities[i]!.get(s.id);
      if (ci === undefined) continue;
      for (let j = i; j < n; j++) {
        const cj = sensitivities[j]!.get(s.id);
        if (cj === undefined) continue;
        const term = ci.mul(cj).mul(u2);
        covariance[i]![j] = covariance[i]![j]!.add(term);
        if (j !== i) covariance[j]![i] = covariance[i]![j]!;
      }
    }
  }

  const inputVariances = covariance.map((row, i) => row[i]!);

  return { inputs, sources, covariance, inputVariances, sensitivities, warnings };
}

/** 便捷构造：一批相互独立的输入，各自携带私有误差源（源 id 取输入 id）。 */
export function independentInputs(
  decls: Array<{ id: string; value: Decimal; std: Decimal }>,
): { sources: SourceDecl[]; inputs: InputDecl[] } {
  const sources: SourceDecl[] = decls.map((d) => ({ id: d.id, std: d.std }));
  const inputs: InputDecl[] = decls.map((d) => ({
    id: d.id,
    value: d.value,
    contributions: [{ source: d.id, coeff: Decimal.one() }],
  }));
  return { sources, inputs };
}
