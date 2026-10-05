/**
 * 总装：编译模型 → 枚举合法配置 → 计算可达组合 → 精确最小覆盖 → 不可达解释 → 自检。
 */

import type { ModelSpec } from './model.ts';
import { compileModel, posLit, negLit } from './model.ts';
import type { PairReq, CoverResult } from './cover.ts';
import { enumerateValidConfigs, computePairs, solveMinCover, isValidConfig } from './cover.ts';
import { explainUnreachable } from './twosat.ts';

export interface UnreachableItem {
  req: PairReq;
  explanation: string[];
}

export interface GenerateResult {
  features: string[];
  clauseCount: number;
  validCount: number;
  totalConfigs: number;
  reachableCount: number;     // 可达两两组合数
  totalPairCombos: number;  // 4 * C(n,2)
  configs: number[];          // 最优配置（位掩码）
  cover: CoverResult;
  unreachable: UnreachableItem[];
}

export function generate(spec: ModelSpec): GenerateResult {
  const model = compileModel(spec);
  const valid = enumerateValidConfigs(model.n, model.clauses);
  if (valid.length === 0) {
    throw new Error('约束系统不可满足：不存在任何合法配置（注意：仅含互斥/蕴含约束时全 0 配置恒为合法，此错误一般不会出现）');
  }
  const { reqs, unreachable } = computePairs(model.n, valid);
  const cover = valid.length > 0
    ? solveMinCover(model, valid, reqs)
    : (null as unknown as CoverResult);
  if (cover.aborted) {
    throw new Error(`分支限界超过节点上限，未能完成最小性证明（当前最优 ${cover.configs.length}，下界 ${cover.lowerBound}）`);
  }

  // 不可达组合逐一解释
  const items: UnreachableItem[] = [];
  for (const r of unreachable) {
    const units = [
      r.vi === 1 ? posLit(r.i) : negLit(r.i),
      r.vj === 1 ? posLit(r.j) : negLit(r.j),
    ];
    const exp = explainUnreachable(units, model.clauses, model.features);
    items.push({
      req: r,
      explanation: exp ?? ['枚举全部合法配置验证：无任何合法配置同时满足该取值组合（约束传播未给出更短的推导链）。'],
    });
  }

  // ---------- 自检：结果必须合法且覆盖全部可达组合 ----------
  for (const c of cover.configs) {
    if (!isValidConfig(c, model.clauses)) {
      throw new Error(`内部错误：输出了非法配置 ${c.toString(2)}`);
    }
  }
  const covered = new Set<string>();
  for (const c of cover.configs) {
    for (const r of reqs) {
      if (((c >> r.i) & 1) === r.vi && ((c >> r.j) & 1) === r.vj) {
        covered.add(`${r.i},${r.j},${r.vi},${r.vj}`);
      }
    }
  }
  if (covered.size !== reqs.length) {
    throw new Error(`内部错误：仅覆盖 ${covered.size}/${reqs.length} 个可达组合`);
  }

  return {
    features: model.features,
    clauseCount: model.clauses.length,
    validCount: valid.length,
    totalConfigs: 1 << model.n,
    reachableCount: reqs.length,
    totalPairCombos: 4 * (model.n * (model.n - 1)) / 2,
    configs: cover.configs,
    cover,
    unreachable: items,
  };
}
