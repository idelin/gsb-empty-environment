/**
 * 分歧定位：在对齐后的节点对上做精确比较（BigInt 有理数 + 单位，绝不使用容差），
 * 找出"分歧原点"（所有已对齐祖先都一致的最浅分歧节点对），
 * 并给出从原点延伸到输出的精确差异链。
 * 若输出不同但没有任何可比对的中间证据，则判定为 unlocatable（无法定位）。
 */

import { align } from './align.ts';
import type { Alignment } from './align.ts';
import type { NodeDef, Pipeline, Trace, TraceEntry } from './dag.ts';
import type { Rat } from './rational.ts';
import type { Unit } from './units.ts';

export type CauseKind =
  | 'rounding-rule'         // 舍入规则/标度不同
  | 'unit-conversion'       // 单位换算口径不同（因子不同或换算缺失）
  | 'denominator-convention'// 分母口径不同（如 n vs n-1）
  | 'constant-value'        // 常量取值不同
  | 'unit-mismatch'         // 维度层面单位错误
  | 'structure'             // 运算结构不同
  | 'unknown';

export type FinalStatus =
  | 'equal'          // 最终输出精确一致（中间分歧见 intermediateOnly）
  | 'different'      // 最终输出数值不同
  | 'unit-mismatch'  // 最终输出单位口径不同（硬性错误，不容差掩盖）
  | 'unlocatable';   // 输出不同但缺少中间证据，无法定位

export interface Divergence {
  aId: string;
  bId: string;
  cause: CauseKind;
  aValue: Rat;
  bValue: Rat;
  aUnit: Unit;
  bUnit: Unit;
  aDetail: string;
  bDetail: string;
  how: 'structural' | 'positional';
}

export interface ChainStep {
  aId: string;
  bId: string;
  a: TraceEntry;
  b: TraceEntry;
}

export interface DifferenceChain {
  origin: Divergence;
  steps: ChainStep[];
  reachesOutput: boolean;
}

export interface ComparisonResult {
  pipelineA: string;
  pipelineB: string;
  inputs: Record<string, Rat>;
  finalStatus: FinalStatus;
  finalA: TraceEntry;
  finalB: TraceEntry;
  origins: Divergence[];
  chains: DifferenceChain[];
  /** finalStatus === 'equal' 时仍存在的中间分歧（最终相同但中间不同，必须记录）。 */
  intermediateOnly: Divergence[];
  /** 因证据缺失（traced:false）而无法比对的已对齐节点对。 */
  missingEvidence: string[];
  unmatchedA: string[];
  unmatchedB: string[];
  alignment: Alignment;
}

function usedAsDivisor(p: Pipeline, id: string): boolean {
  return p.consumers(id).some((c) => {
    const nd = p.node(c);
    return nd.kind === 'div' && nd.args[1] === id;
  });
}

function classify(a: Pipeline, b: Pipeline, aId: string, bId: string, ea: TraceEntry, eb: TraceEntry): CauseKind {
  const ua = ea.unit as Unit;
  const ub = eb.unit as Unit;
  if (!ua.sameDim(ub)) return 'unit-mismatch';
  const ndA = a.node(aId);
  const ndB = b.node(bId);
  if (ndA.kind === 'round' && ndB.kind === 'round') return 'rounding-rule';
  if (ndA.kind === 'convert' && ndB.kind === 'convert') return 'unit-conversion';
  if (!ua.same(ub)) return 'unit-conversion'; // 同维度但换算因子不同
  if (ndA.kind === 'const' && ndB.kind === 'const') {
    return usedAsDivisor(a, aId) || usedAsDivisor(b, bId) ? 'denominator-convention' : 'constant-value';
  }
  if (ndA.kind !== ndB.kind) return 'structure';
  return 'unknown';
}

export function comparePipelines(a: Pipeline, b: Pipeline, inputs: Record<string, Rat>): ComparisonResult {
  const ta = a.evaluate(inputs);
  const tb = b.evaluate(inputs);
  const alignment = align(a, b);

  const divergent: { pair: { a: string; b: string; how: 'structural' | 'positional' }; ea: TraceEntry; eb: TraceEntry; cause: CauseKind }[] = [];
  const missingEvidence: string[] = [];

  for (const pr of alignment.pairs) {
    const ea = ta.get(pr.a);
    const eb = tb.get(pr.b);
    if (!ea.evidence || !eb.evidence) {
      missingEvidence.push(`${a.name}:${pr.a} ~ ${b.name}:${pr.b}`);
      continue;
    }
    const sameValue = (ea.value as Rat).eq(eb.value as Rat);
    const sameUnit = (ea.unit as Unit).same(eb.unit as Unit);
    if (!sameValue || !sameUnit) {
      divergent.push({ pair: pr, ea, eb, cause: classify(a, b, pr.a, pr.b, ea, eb) });
    }
  }

  // 分歧原点：不存在任何已分歧的祖先节点对
  const divA = new Set(divergent.map((d) => d.pair.a));
  const divB = new Set(divergent.map((d) => d.pair.b));
  const origins: Divergence[] = divergent
    .filter((d) => {
      const ancA = a.ancestors(d.pair.a);
      const ancB = b.ancestors(d.pair.b);
      for (const x of ancA) if (divA.has(x)) return false;
      for (const y of ancB) if (divB.has(y)) return false;
      return true;
    })
    .map((d) => ({
      aId: d.pair.a,
      bId: d.pair.b,
      cause: d.cause,
      aValue: d.ea.value as Rat,
      bValue: d.eb.value as Rat,
      aUnit: d.ea.unit as Unit,
      bUnit: d.eb.unit as Unit,
      aDetail: d.ea.detail,
      bDetail: d.eb.detail,
      how: d.pair.how,
    }));

  // 差异链：从每个原点沿"已对齐且已分歧"的边走向输出
  const byA = new Map(divergent.map((d) => [d.pair.a, d]));
  const chains: DifferenceChain[] = origins.map((origin) => {
    const steps: ChainStep[] = [];
    let cur = origin.aId;
    const firstDiv = byA.get(cur);
    if (firstDiv) steps.push({ aId: firstDiv.pair.a, bId: firstDiv.pair.b, a: firstDiv.ea, b: firstDiv.eb });
    while (cur !== a.outputId) {
      const next = a.consumers(cur).find((c) => byA.has(c));
      if (!next) break;
      const d = byA.get(next) as (typeof divergent)[number];
      steps.push({ aId: d.pair.a, bId: d.pair.b, a: d.ea, b: d.eb });
      cur = next;
    }
    return { origin, steps, reachesOutput: cur === a.outputId };
  });

  // 最终输出直接对比（不依赖对齐）
  const finalA = ta.output;
  const finalB = tb.output;
  let finalStatus: FinalStatus;
  if (!finalA.evidence || !finalB.evidence) {
    finalStatus = 'unlocatable';
  } else if (!(finalA.unit as Unit).sameDim(finalB.unit as Unit)) {
    finalStatus = 'unit-mismatch';
  } else if (!(finalA.unit as Unit).same(finalB.unit as Unit)) {
    finalStatus = 'unit-mismatch'; // 同维度但因子口径不同（如 m vs cm），按单位错误处理
  } else if (!(finalA.value as Rat).eq(finalB.value as Rat)) {
    finalStatus = 'different';
  } else {
    finalStatus = 'equal';
  }
  if (finalStatus === 'different' && origins.length === 0) {
    // 数值不同但没有任何可比对的中间分歧原点：缺少中间证据，无法定位。
    // （unit-mismatch 在输出处即可直接观测，属于已定位，不在此列。）
    finalStatus = 'unlocatable';
  }

  return {
    pipelineA: a.name,
    pipelineB: b.name,
    inputs,
    finalStatus,
    finalA,
    finalB,
    origins,
    chains,
    intermediateOnly: finalStatus === 'equal' ? origins : [],
    missingEvidence,
    unmatchedA: alignment.unmatchedA,
    unmatchedB: alignment.unmatchedB,
    alignment,
  };
}
