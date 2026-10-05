/**
 * 导出脱敏检查器。
 *
 * 核心规则：
 *  1. 敏感级别沿字段计算依赖传播：派生字段继承所有输入的最高级别，
 *     因此"隐藏原字段"（不导出 source）并不能消除派生字段的泄露。
 *  2. 聚合降级：仅当聚合的每个分组人数都 >= minGroupSize 时，
 *     输出才允许降级到 downgradeTo；否则保留传播来的原始级别。
 *  3. 过滤会缩减人数：过滤节点减少行数，可能使下游聚合不再满足
 *     最小分组人数，从而令原本合法的降级失效。
 *  4. 共享计算节点：多个导出字段引用同一子图时，该节点只求值一次，
 *     其泄露会同时出现在多条违规来源链中。
 */

import { LEVEL_NAMES } from "./model.ts";
import type {
  ExportPlan,
  NodeId,
  PlanNode,
  SensitivityLevel,
} from "./model.ts";

/** 单个节点的求值结果 */
export interface NodeEvaluation {
  nodeId: NodeId;
  field: string;
  kind: PlanNode["kind"];
  /** 沿依赖传播而来的级别（未考虑本节点降级） */
  inheritedLevel: SensitivityLevel;
  /** 本节点输出的生效级别 */
  effectiveLevel: SensitivityLevel;
  /** 该节点处的人数（聚合节点为总人数） */
  population: number;
  /** 聚合节点：降级是否生效及原因 */
  downgrade?: {
    applied: boolean;
    minGroupSize: number;
    actualMinGroup: number;
    reason: string;
  };
}

/** 来源链上的一步 */
export interface ChainStep {
  nodeId: NodeId;
  field: string;
  kind: PlanNode["kind"];
  level: SensitivityLevel;
  note?: string;
}

/** 一条违规：导出字段超出目标环境允许级别，附全部责任来源链 */
export interface Violation {
  exportField: string;
  exportNodeId: NodeId;
  effectiveLevel: SensitivityLevel;
  allowedLevel: SensitivityLevel;
  /** 从导出字段回溯到原始敏感字段的来源链（可能多条） */
  chains: ChainStep[][];
}

export interface CheckReport {
  planName: string;
  evaluations: Map<NodeId, NodeEvaluation>;
  violations: Violation[];
  /** 被 >=2 个导出字段共享的计算节点 id */
  sharedNodes: NodeId[];
  /** 求值计数：验证共享节点只计算一次 */
  evaluationCounts: Map<NodeId, number>;
  ok: boolean;
}

function maxLevel(levels: SensitivityLevel[]): SensitivityLevel {
  return Math.max(...levels) as SensitivityLevel;
}

export function checkExportPlan(plan: ExportPlan): CheckReport {
  const nodes = new Map<NodeId, PlanNode>();
  for (const node of plan.nodes) {
    if (nodes.has(node.id)) throw new Error(`重复节点 id: ${node.id}`);
    nodes.set(node.id, node);
  }

  const evaluations = new Map<NodeId, NodeEvaluation>();
  const evaluationCounts = new Map<NodeId, number>();
  const visiting = new Set<NodeId>();

  function evaluate(id: NodeId): NodeEvaluation {
    const cached = evaluations.get(id);
    if (cached) return cached;
    const node = nodes.get(id);
    if (!node) throw new Error(`未知节点: ${id}`);
    if (visiting.has(id)) throw new Error(`计算图存在环，涉及节点: ${id}`);
    visiting.add(id);
    evaluationCounts.set(id, (evaluationCounts.get(id) ?? 0) + 1);

    let result: NodeEvaluation;
    switch (node.kind) {
      case "source": {
        result = {
          nodeId: id,
          field: node.field,
          kind: node.kind,
          inheritedLevel: node.level,
          effectiveLevel: node.level,
          population: node.population,
        };
        break;
      }
      case "derive": {
        const inputs = node.inputs.map(evaluate);
        const inherited = maxLevel(inputs.map((i) => i.effectiveLevel));
        result = {
          nodeId: id,
          field: node.field,
          kind: node.kind,
          inheritedLevel: inherited,
          effectiveLevel: inherited,
          population: Math.min(...inputs.map((i) => i.population)),
        };
        break;
      }
      case "filter": {
        const input = evaluate(node.inputs[0]);
        if (node.remainingPopulation > input.population) {
          throw new Error(
            `过滤节点 ${id} 的剩余人数 ${node.remainingPopulation} 超过输入人数 ${input.population}`,
          );
        }
        result = {
          nodeId: id,
          field: node.field,
          kind: node.kind,
          inheritedLevel: input.effectiveLevel,
          effectiveLevel: input.effectiveLevel,
          population: node.remainingPopulation,
        };
        break;
      }
      case "aggregate": {
        const input = evaluate(node.inputs[0]);
        const inherited = input.effectiveLevel;
        const actualMinGroup = Math.min(...node.groupSizes);
        const total = node.groupSizes.reduce((a, b) => a + b, 0);
        const applied = actualMinGroup >= node.minGroupSize;
        const effective = applied
          ? (Math.min(inherited, node.downgradeTo) as SensitivityLevel)
          : inherited;
        result = {
          nodeId: id,
          field: node.field,
          kind: node.kind,
          inheritedLevel: inherited,
          effectiveLevel: effective,
          population: total,
          downgrade: {
            applied,
            minGroupSize: node.minGroupSize,
            actualMinGroup,
            reason: applied
              ? `最小组人数 ${actualMinGroup} >= ${node.minGroupSize}，降级至 ${LEVEL_NAMES[effective]}`
              : `最小组人数 ${actualMinGroup} < ${node.minGroupSize}，降级被拒绝，保持 ${LEVEL_NAMES[inherited]}`,
          },
        };
        break;
      }
    }

    visiting.delete(id);
    evaluations.set(id, result);
    return result;
  }

  /** 回溯来源链：只沿"贡献了最高级别"的输入边上溯，终止于 source 节点 */
  function traceChains(id: NodeId): ChainStep[][] {
    const node = nodes.get(id)!;
    const ev = evaluations.get(id)!;
    const step: ChainStep = { nodeId: id, field: node.field, kind: node.kind, level: ev.effectiveLevel };

    if (node.kind === "aggregate" && ev.downgrade && !ev.downgrade.applied) {
      step.note = `聚合降级未生效：${ev.downgrade.reason}`;
    }
    if (node.kind === "filter") {
      const input = evaluations.get(node.inputs[0])!;
      step.note = `过滤 ${node.predicate}：人数 ${input.population} -> ${ev.population}`;
    }
    if (node.kind === "source") {
      return [[step]];
    }

    const inputEvals = node.inputs.map((inputId) => evaluations.get(inputId)!);
    const carrier = maxLevel(inputEvals.map((e) => e.effectiveLevel));
    const chains: ChainStep[][] = [];
    node.inputs.forEach((inputId, idx) => {
      // 只追踪级别承载边：低级别输入不是泄露来源
      if (inputEvals[idx].effectiveLevel >= carrier) {
        for (const tail of traceChains(inputId)) {
          chains.push([step, ...tail]);
        }
      }
    });
    return chains;
  }

  // 求值所有导出字段（共享子图因缓存只求值一次）
  const violations: Violation[] = [];
  for (const req of plan.exports) {
    const ev = evaluate(req.nodeId);
    if (ev.effectiveLevel > req.allowedLevel) {
      violations.push({
        exportField: ev.field,
        exportNodeId: req.nodeId,
        effectiveLevel: ev.effectiveLevel,
        allowedLevel: req.allowedLevel,
        chains: traceChains(req.nodeId),
      });
    }
  }

  // 共享计算节点：从每个导出字段出发的可达集出现 >=2 次的非 source 节点
  const reachCount = new Map<NodeId, number>();
  function markReachable(id: NodeId, seen: Set<NodeId>): void {
    if (seen.has(id)) return;
    seen.add(id);
    const node = nodes.get(id)!;
    if (node.kind !== "source") {
      for (const inputId of node.inputs) markReachable(inputId, seen);
    }
  }
  for (const req of plan.exports) {
    const seen = new Set<NodeId>();
    markReachable(req.nodeId, seen);
    for (const id of seen) reachCount.set(id, (reachCount.get(id) ?? 0) + 1);
  }
  const sharedNodes = [...reachCount.entries()]
    .filter(([, count]) => count >= 2)
    .map(([id]) => id);

  return {
    planName: plan.name,
    evaluations,
    violations,
    sharedNodes,
    evaluationCounts,
    ok: violations.length === 0,
  };
}

/** 将报告渲染为可读文本 */
export function formatReport(report: CheckReport): string {
  const lines: string[] = [];
  lines.push(`导出计划「${report.planName}」检查结果: ${report.ok ? "通过" : "存在违规"}`);
  for (const v of report.violations) {
    lines.push(
      `违规: 导出字段 ${v.exportField} 生效级别 ${LEVEL_NAMES[v.effectiveLevel]} 超过允许级别 ${LEVEL_NAMES[v.allowedLevel]}`,
    );
    v.chains.forEach((chain, i) => {
      lines.push(`  来源链 ${i + 1}:`);
      for (const step of chain) {
        const note = step.note ? `  [${step.note}]` : "";
        lines.push(`    ${step.field} (${step.kind}, ${LEVEL_NAMES[step.level]})${note}`);
      }
    });
  }
  if (report.sharedNodes.length > 0) {
    lines.push(`共享计算节点: ${report.sharedNodes.join(", ")}`);
  }
  return lines.join("\n");
}
