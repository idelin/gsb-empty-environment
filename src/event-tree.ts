import { Fraction } from "./fraction.ts";
import { PrivacyGate } from "./privacy-gate.ts";
import type { PublicObservation, ResourceEvent } from "./privacy-gate.ts";

/**
 * 自适应策略：根据当前公开证据选择下一个已签模块；返回 null 表示该分支结束。
 * 这正是“模块可按已见结果自适应选择”的建模入口。
 */
export type TreeStrategy = (evidence: PublicObservation[]) => ResourceEvent | null;

export interface TreeNode {
  depth: number;
  /** 从根到该节点的公开资源序列（null 为空观测）。 */
  path: (string | null)[];
  evidence: PublicObservation[];
  /** 该路径在先验混合分布下的精确概率。 */
  mixtureProbability: Fraction;
  posteriors: Record<string, Fraction>;
  ratios: Record<string, Fraction>;
  worstSecret: string;
  worstPosterior: Fraction;
  worstRatio: Fraction;
  /** 宿主是否放行；拒绝时宿主状态保持不变，该节点成为叶子。 */
  admitted: boolean;
  rejectionResource: string | null;
  /** 该节点对应的假设结果本身是否会越过 bound（拒绝叶子为 true）。 */
  wouldViolate: boolean;
}

export interface EnumerationReport {
  nodes: TreeNode[];
  leaves: TreeNode[];
  /** 可达路径的混合概率之和（被宿主截断的子树不计入）。 */
  reachableMass: Fraction;
  /** 所有可达节点中的最坏节点（按 posterior/prior）。 */
  worst: TreeNode;
  allWithinBound: boolean;
}

/**
 * 小事件树穷举：在每个节点枚举模块所有可区分的公开结果，
 * 跳过混合概率为 0 的零概率分支（全程精确分数，不做浮点除零），
 * 直到策略返回 null 或达到 maxDepth。
 */
export function enumerateTree(
  gate: PrivacyGate,
  strategy: TreeStrategy,
  maxDepth: number,
): EnumerationReport {
  const rootGate = gate.clone();
  const nodes: TreeNode[] = [];
  const leaves: TreeNode[] = [];
  let reachableMass = Fraction.ZERO;

  const rootPosterior = rootGate.posteriors();
  const root: TreeNode = {
    depth: 0,
    path: [],
    evidence: [],
    mixtureProbability: Fraction.ONE,
    posteriors: rootPosterior,
    ratios: Object.fromEntries(Object.keys(rootPosterior).map((secret) => [secret, Fraction.ONE])),
    worstSecret: rootGate.currentWorst().secret,
    worstPosterior: rootGate.currentWorst().posterior,
    worstRatio: Fraction.ONE,
    admitted: true,
    rejectionResource: null,
    wouldViolate: false,
  };
  nodes.push(root);
  if (maxDepth === 0) {
    leaves.push(root);
    reachableMass = reachableMass.add(root.mixtureProbability);
  }

  const walk = (node: TreeNode, nodeGate: PrivacyGate): void => {
    if (node.depth >= maxDepth) {
      leaves.push(node);
      return;
    }
    const nextEvent = strategy(nodeGate.publicLog());
    if (nextEvent === null) {
      leaves.push(node);
      return;
    }
    const evaluation = nodeGate.evaluateNew(nextEvent);
    if (evaluation.violates) {
      const worstOutcome = evaluation.outcomes.reduce((acc, outcome) =>
        outcome.worstRatio.compareTo(acc.worstRatio) > 0 ? outcome : acc,
      );
      leaves.push({
        ...node,
        depth: node.depth + 1,
        path: [...node.path, nodeGate.rejectionResourceId],
        admitted: false,
        rejectionResource: nodeGate.rejectionResourceId,
        wouldViolate: true,
        worstSecret: worstOutcome.worstSecret,
        worstPosterior: worstOutcome.worstPosterior,
        worstRatio: worstOutcome.worstRatio,
      });
      nodes.push(leaves[leaves.length - 1]);
      return;
    }
    const parentLikelihoods = nodeGate.likelihoods();
    let extended = false;
    for (const outcome of evaluation.outcomes) {
      const report = nodeGate.hypothetical(nextEvent, outcome.resourceId);
      const branchMass = branchMixtureMass(node, report, parentLikelihoods);
      if (branchMass.isZero()) continue;
      const childGate = nodeGate.clone();
      const childProposal = childGate.propose({
        ...nextEvent,
        observedResourceId: outcome.resourceId,
      });
      if (!childProposal.admitted) {
        throw new Error("enumerateTree: 预检与实际放行结果不一致");
      }
      const child: TreeNode = {
        depth: node.depth + 1,
        path: [...node.path, outcome.resourceId],
        evidence: childGate.publicLog(),
        mixtureProbability: branchMass,
        posteriors: report.posteriors,
        ratios: report.ratios,
        worstSecret: report.worst.secret,
        worstPosterior: report.worst.posterior,
        worstRatio: report.worst.ratio,
        admitted: true,
        rejectionResource: null,
        wouldViolate: false,
      };
      nodes.push(child);
      reachableMass = reachableMass.add(child.mixtureProbability);
      extended = true;
      walk(child, childGate);
    }
    if (!extended) leaves.push(node);
  };

  walk(root, rootGate);

  let worst = nodes[0];
  for (const node of nodes.slice(1)) {
    if (node.worstRatio.compareTo(worst.worstRatio) > 0) worst = node;
  }
  const allWithinBound = nodes.every((node) => !node.wouldViolate);
  return { nodes, leaves, reachableMass, worst, allWithinBound };
}

/**
 * 分支混合概率 = 父路径混合概率 * 本步新结果的混合条件概率。
 * hypothetical 返回完整联合似然 L_s(父证据 ∧ 新结果)，
 * 本步条件因子为 L_s(父∧新) / L_s(父)，再按父后验加权求和。
 * 父似然为 0 的秘密在父路径已被排除，直接跳过。
 */
function branchMixtureMass(
  parent: TreeNode,
  report: ReturnType<PrivacyGate["hypothetical"]>,
  parentLikelihoods: Record<string, Fraction>,
): Fraction {
  let conditionalMass = Fraction.ZERO;
  for (const [secretId, parentLikelihood] of Object.entries(parentLikelihoods)) {
    if (parentLikelihood.isZero()) continue;
    const stepFactor = report.likelihoods[secretId].div(parentLikelihood);
    conditionalMass = conditionalMass.add(parent.posteriors[secretId].mul(stepFactor));
  }
  return parent.mixtureProbability.mul(conditionalMass);
}
