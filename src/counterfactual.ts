/**
 * 反事实确认：把流水 A 中疑似致因的规则（舍入规则/换算因子/分母常量）
 * 替换为流水 B 的对应规则，独立重算 A'，若 A' 输出与 B 精确一致，
 * 则该规则子集被确认为导致输出差异的（最小）原因。
 */

import type { ComparisonResult, Divergence } from './compare.ts';
import { Pipeline } from './dag.ts';
import type { NodeDef, RoundSpec } from './dag.ts';
import { Rat } from './rational.ts';
import { Unit } from './units.ts';

export type RuleEdit =
  | { nodeId: string; type: 'rounding'; spec: RoundSpec }
  | { nodeId: string; type: 'convert-factor'; factor: Rat }
  | { nodeId: string; type: 'constant'; value: Rat };

export function describeEdit(p: Pipeline, e: RuleEdit): string {
  switch (e.type) {
    case 'rounding':
      return `${p.name}:${e.nodeId} 舍入规则 → ${e.spec.rule} @${e.spec.scale}dp`;
    case 'convert-factor':
      return `${p.name}:${e.nodeId} 换算因子 → ×${e.factor.display()}`;
    case 'constant':
      return `${p.name}:${e.nodeId} 常量 → ${e.value.display()}`;
  }
}

/** 生成应用了规则替换的新流水（原流水不变）。 */
export function applyEdits(p: Pipeline, edits: RuleEdit[]): Pipeline {
  const nodes: NodeDef[] = p.def.nodes.map((nd) => {
    const edit = edits.find((e) => e.nodeId === nd.id);
    if (!edit) return nd;
    if (edit.type === 'rounding' && nd.kind === 'round') return { ...nd, spec: edit.spec };
    if (edit.type === 'convert-factor' && nd.kind === 'convert') {
      const to: Unit = Unit.define(`${nd.to.name}'`, { ...nd.to.dims }, edit.factor);
      return { ...nd, to };
    }
    if (edit.type === 'constant' && nd.kind === 'const') return { ...nd, value: edit.value };
    throw new Error(`编辑 ${edit.type} 与节点 ${nd.id}（${nd.kind}）不兼容`);
  });
  return new Pipeline({ ...p.def, nodes });
}

/** 从分歧原点推导反事实编辑：把 A 的规则换成 B 的规则。 */
export function deriveEdit(origin: Divergence, b: Pipeline): RuleEdit {
  const ndB = b.node(origin.bId);
  if (ndB.kind === 'round') return { nodeId: origin.aId, type: 'rounding', spec: ndB.spec };
  if (ndB.kind === 'convert') return { nodeId: origin.aId, type: 'convert-factor', factor: ndB.to.factor };
  if (ndB.kind === 'const') return { nodeId: origin.aId, type: 'constant', value: ndB.value };
  throw new Error(`无法为节点类型 ${ndB.kind} 推导反事实编辑`);
}

export interface Explanation {
  edits: RuleEdit[];
  descriptions: string[];
  /** 重算后 A' 输出与 B 输出精确一致（值+单位）。 */
  confirmed: boolean;
  /** 未确认时的残差（A' 输出与 B 输出的精确差异）。 */
  residual?: { aPrime: string; b: string };
}

function outputsMatch(aPrime: Pipeline, b: Pipeline, inputs: Record<string, Rat>): { match: boolean; aPrimeOut: string; bOut: string } {
  const fa = aPrime.evaluate(inputs).output;
  const fb = b.evaluate(inputs).output;
  const match =
    fa.evidence &&
    fb.evidence &&
    (fa.unit as Unit).same(fb.unit as Unit) &&
    (fa.value as Rat).eq(fb.value as Rat);
  const fmt = (v: Rat | null, u: Unit | null): string =>
    v === null || u === null ? '<无证据>' : `${v.display()} [${u.name}]`;
  return { match, aPrimeOut: fmt(fa.value, fa.unit), bOut: fmt(fb.value, fb.unit) };
}

/**
 * 寻找最小已确认规则子集：先试单点编辑，再按 maxSubsetSize 逐步扩大组合。
 * 每个候选都独立重算，确认标准是与 B 的输出精确相等（值+单位）。
 */
export function explainDivergence(
  a: Pipeline,
  b: Pipeline,
  inputs: Record<string, Rat>,
  cmp: ComparisonResult,
  maxSubsetSize = 2,
): Explanation[] {
  const explanations: Explanation[] = [];
  const candidateEdits: RuleEdit[] = [];
  for (const origin of cmp.origins) {
    try {
      candidateEdits.push(deriveEdit(origin, b));
    } catch {
      // 该原点类型不支持反事实替换（如结构差异），跳过
    }
  }

  const trySubset = (edits: RuleEdit[]): Explanation => {
    const aPrime = applyEdits(a, edits);
    const { match, aPrimeOut, bOut } = outputsMatch(aPrime, b, inputs);
    return {
      edits,
      descriptions: edits.map((e) => describeEdit(a, e)),
      confirmed: match,
      residual: match ? undefined : { aPrime: aPrimeOut, b: bOut },
    };
  };

  // 单点
  for (const e of candidateEdits) explanations.push(trySubset([e]));
  if (explanations.some((x) => x.confirmed)) return explanations.filter((x) => x.confirmed);

  // 组合（规模 2..maxSubsetSize）
  const combine = (arr: RuleEdit[], k: number, start: number, acc: RuleEdit[], out: RuleEdit[][]): void => {
    if (acc.length === k) {
      out.push([...acc]);
      return;
    }
    for (let i = start; i < arr.length; i++) {
      acc.push(arr[i]);
      combine(arr, k, i + 1, acc, out);
      acc.pop();
    }
  };
  for (let k = 2; k <= Math.min(maxSubsetSize, candidateEdits.length); k++) {
    const subsets: RuleEdit[][] = [];
    combine(candidateEdits, k, 0, [], subsets);
    const results = subsets.map(trySubset);
    explanations.push(...results);
    const confirmed = results.filter((x) => x.confirmed);
    if (confirmed.length > 0) return confirmed;
  }
  return explanations;
}
