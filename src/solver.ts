/**
 * 带权最大约束满足(Weighted Max-CSP)分支定界求解器。
 *
 * 问题模型:一组有限域变量、一组必须满足的硬约束、
 * 一组带惩罚权重的软约束。目标:在满足全部硬约束的前提下,
 * 找到使"被违反软约束的权重之和"最小的赋值(全局最优)。
 */

export type Assignment = Record<string, number>;

export interface Variable {
  name: string;
  domain: number[];
}

export interface HardConstraint {
  name: string;
  /** 涉及的变量名 */
  scope: string[];
  /**
   * 部分赋值一致性检查:仅当当前部分赋值"已经不可能"扩展为
   * 满足该约束的完整赋值时返回 false(用于提前剪枝)。
   */
  consistent(assignment: Assignment): boolean;
}

export interface SoftConstraint {
  name: string;
  /** 违反该约束的惩罚权重 */
  weight: number;
  scope: string[];
  /** 完整赋值下是否被违反(部分赋值时未被完全实例化应返回 false) */
  violated(assignment: Assignment): boolean;
}

export interface MaxCSPProblem {
  variables: Variable[];
  hard: HardConstraint[];
  soft: SoftConstraint[];
}

export interface SolveStats {
  /** 分支定界展开的搜索节点数 */
  nodes: number;
  /** 因下界不优于当前最优而被剪掉的子树次数 */
  pruned: number;
}

export interface SolveResult {
  /** 最优赋值;硬约束不可满足时为 null */
  assignment: Assignment | null;
  /** 最小总惩罚;无可行解时为 Infinity */
  cost: number;
  /** 最优解中被违反的软约束名 */
  violated: string[];
  stats: SolveStats;
}

/**
 * 分支定界求解。
 *
 * - 分支:深度优先,按"约束度最大优先"排序变量;取值按
 *   "即时惩罚增量"升序尝试,尽早获得优质 incumbent。
 * - 定界:下界 = 已完全实例化且被违反的软约束权重和
 *   (未完全实例化的软约束乐观估计为 0,保证下界可采纳)。
 * - 剪枝:下界 >= 当前最优代价时剪去子树;硬约束一致性
 *   检查失败的分支直接不展开。
 */
export function solve(problem: MaxCSPProblem): SolveResult {
  const { variables, hard, soft } = problem;

  const hardByVar = indexByVar(hard);
  const softByVar = indexByVar(soft);

  // 变量排序:关联约束越多越靠前(最约束优先),静态一次计算
  const degree = (name: string): number =>
    (hardByVar.get(name)?.length ?? 0) + (softByVar.get(name)?.length ?? 0);
  const order = [...variables].sort((a, b) => degree(b.name) - degree(a.name));

  const assignment: Assignment = {};
  let best = Infinity;
  let bestAssignment: Assignment | null = null;
  const stats: SolveStats = { nodes: 0, pruned: 0 };

  // 试算 var=value 后,新变为"完全实例化且被违反"的软约束权重和
  const deltaPenalty = (name: string, value: number): number => {
    assignment[name] = value;
    let delta = 0;
    for (const sc of softByVar.get(name) ?? []) {
      if (sc.scope.every((v) => assignment[v] !== undefined) && sc.violated(assignment)) {
        delta += sc.weight;
      }
    }
    delete assignment[name];
    return delta;
  };

  // 试算 var=value 后所有相关硬约束是否仍可能满足
  const hardOk = (name: string, value: number): boolean => {
    assignment[name] = value;
    const ok = (hardByVar.get(name) ?? []).every((hc) => hc.consistent(assignment));
    delete assignment[name];
    return ok;
  };

  const dfs = (idx: number, bound: number): void => {
    if (bound >= best) {
      stats.pruned++;
      return;
    }
    if (idx === order.length) {
      best = bound;
      bestAssignment = { ...assignment };
      return;
    }
    stats.nodes++;
    const variable = order[idx];
    const candidates = variable.domain
      .filter((value) => hardOk(variable.name, value))
      .map((value) => ({ value, delta: deltaPenalty(variable.name, value) }))
      .sort((a, b) => a.delta - b.delta);
    for (const { value, delta } of candidates) {
      assignment[variable.name] = value;
      dfs(idx + 1, bound + delta);
      delete assignment[variable.name];
    }
  };

  dfs(0, 0);

  return {
    assignment: bestAssignment,
    cost: best,
    violated: bestAssignment
      ? soft.filter((sc) => sc.violated(bestAssignment)).map((sc) => sc.name)
      : [],
    stats,
  };
}

/** 暴力枚举全部赋值,返回全局最优(用于交叉验证分支定界结果)。 */
export function bruteForce(problem: MaxCSPProblem): SolveResult {
  const { variables, hard, soft } = problem;
  const assignment: Assignment = {};
  let best = Infinity;
  let bestAssignment: Assignment | null = null;
  const stats: SolveStats = { nodes: 0, pruned: 0 };

  const dfs = (idx: number): void => {
    if (idx === variables.length) {
      stats.nodes++;
      if (!hard.every((hc) => hc.consistent(assignment))) return;
      const cost = soft.reduce(
        (sum, sc) => sum + (sc.violated(assignment) ? sc.weight : 0),
        0,
      );
      if (cost < best) {
        best = cost;
        bestAssignment = { ...assignment };
      }
      return;
    }
    const variable = variables[idx];
    for (const value of variable.domain) {
      assignment[variable.name] = value;
      dfs(idx + 1);
      delete assignment[variable.name];
    }
  };

  dfs(0);

  return {
    assignment: bestAssignment,
    cost: best,
    violated: bestAssignment
      ? soft.filter((sc) => sc.violated(bestAssignment)).map((sc) => sc.name)
      : [],
    stats,
  };
}

/** 计算某赋值的总惩罚(软约束违反权重和)。 */
export function totalPenalty(soft: SoftConstraint[], assignment: Assignment): number {
  return soft.reduce((sum, sc) => sum + (sc.violated(assignment) ? sc.weight : 0), 0);
}

function indexByVar<T extends { scope: string[] }>(constraints: T[]): Map<string, T[]> {
  const map = new Map<string, T[]>();
  for (const c of constraints) {
    for (const v of c.scope) {
      const list = map.get(v);
      if (list) list.push(c);
      else map.set(v, [c]);
    }
  }
  return map;
}
